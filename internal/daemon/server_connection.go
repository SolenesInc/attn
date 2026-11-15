package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type serverConnection struct {
	d           *Daemon
	socket      string
	ensureMu    sync.Mutex
	uses        int
	mu          sync.Mutex
	control     *effectrpc.Client
	info        effectrpc.ServerInfo
	ready       chan struct{}
	readyClosed bool
	terminal    harness.TerminalID
	events      linkEvents
	busy        map[string]string
	ended       map[string]serverEnded
}

func (l *serverLink) connection() (*serverConnection, error) {
	socket, err := config.CanonicalRuntimePath(l.socketPath())
	if err != nil {
		return nil, err
	}
	if socket == "" {
		return nil, &effectrpc.Error{Server: l.b.agent, Method: "initialize", Message: "server binding unavailable until plugin " + l.b.plugin + " registers"}
	}
	d := l.d
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	if p := d.serverConnections[socket]; p != nil {
		return p, nil
	}
	p := &serverConnection{d: d, socket: socket, ended: make(map[string]serverEnded)}
	if d.serverConnections == nil {
		d.serverConnections = make(map[string]*serverConnection)
	}
	d.serverConnections[socket] = p
	d.life.Go("appServerStop", func() {
		<-d.life.Done()
		if c := p.client(); c != nil {
			c.Close()
		}
	})
	return p, nil
}

func (p *serverConnection) client() *effectrpc.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.control
}

func (p *serverConnection) peers() []*serverLink {
	p.mu.Lock()
	info := p.info
	p.mu.Unlock()
	var peers []*serverLink
	for _, l := range p.d.appServerLinks() {
		socket, err := config.CanonicalRuntimePath(l.socketPath())
		if err != nil || socket != p.socket {
			continue
		}
		if info.Protocol > 0 && l.validateServerInfo(info, p.socket) != nil {
			continue
		}
		peers = append(peers, l)
	}
	return peers
}

func (p *serverConnection) sessions() map[protocol.SessionID]*serverLink {
	peers := make(map[string]*serverLink)
	for _, l := range p.peers() {
		peers[l.b.kind] = l
	}
	sessions := make(map[protocol.SessionID]*serverLink)
	for _, s := range p.d.store.List("") {
		if l := peers[p.d.store.SessionLink(s.ID)]; l != nil {
			sessions[s.ID] = l
		}
	}
	return sessions
}

func (p *serverConnection) ensure(ctx context.Context, l *serverLink, opts *ptybackend.SpawnOptions, start bool) (*effectrpc.Client, error) {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	return p.ensureLocked(ctx, l, opts, start)
}

func (p *serverConnection) ensureLocked(ctx context.Context, l *serverLink, opts *ptybackend.SpawnOptions, start bool) (*effectrpc.Client, error) {
	p.mu.Lock()
	c, info := p.control, p.info
	p.mu.Unlock()
	if c != nil {
		if err := l.validateServerInfo(info, p.socket); err != nil {
			return nil, err
		}
		return c, nil
	}
	deadline := time.Now().Add(serverStartLimit)
	started := false
	for {
		attempt, cancel := context.WithTimeout(ctx, serverCallLimit)
		c, info, err := l.connect(attempt, p.socket, "attn", buildinfo.Version)
		cancel()
		if err == nil {
			p.hold(c, info)
			return c, nil
		}
		var refused *effectrpc.Error
		if !start || errors.As(err, &refused) || ctx.Err() != nil {
			return nil, err
		}
		if !started {
			p.mu.Lock()
			if p.terminal == "" {
				p.terminal = l.b.terminal
			}
			terminal := p.terminal
			p.mu.Unlock()
			if err := l.startServer(ctx, opts, terminal); err != nil {
				return nil, err
			}
			started = true
		} else if time.Now().After(deadline) {
			return nil, fmt.Errorf("the %s server at %s did not answer within %s: %w", l.b.name, p.socket, serverStartLimit, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (p *serverConnection) borrow(ctx context.Context, l *serverLink, start bool) (*effectrpc.Client, func(), error) {
	p.ensureMu.Lock()
	c, err := p.ensureLocked(ctx, l, nil, start)
	if err != nil {
		p.ensureMu.Unlock()
		return nil, nil, err
	}
	p.uses++
	p.ensureMu.Unlock()
	return c, func() { p.ensureMu.Lock(); p.uses--; p.releaseLocked(); p.ensureMu.Unlock() }, nil
}

func (p *serverConnection) releaseUnlessUsed() {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	p.releaseLocked()
}

func (p *serverConnection) releaseLocked() {
	if p.uses > 0 || len(p.sessions()) > 0 {
		return
	}
	p.mu.Lock()
	c := p.control
	p.control = nil
	p.finishSnapshotLocked()
	p.mu.Unlock()
	if c != nil {
		c.Close()
		p.d.logf("let go of app-server at %s; no session uses it", p.socket)
	}
}

func (p *serverConnection) hold(c *effectrpc.Client, info effectrpc.ServerInfo) {
	p.mu.Lock()
	p.control, p.info = c, info
	p.ready = make(chan struct{})
	p.readyClosed = false
	p.mu.Unlock()
	if !p.d.life.Go("appServerStatus", func() { p.watch(c) }) {
		c.Close()
	}
}

func (p *serverConnection) finishSnapshotLocked() {
	if p.ready != nil && !p.readyClosed {
		close(p.ready)
		p.readyClosed = true
	}
}

func (p *serverConnection) watch(c *effectrpc.Client) {
	err := c.Stream(p.d.life.Context(), "status/watch", p.statusWatchParams(), func(item json.RawMessage) {
		var tree serverTree
		if json.Unmarshal(item, &tree) == nil {
			p.events.run(p.d, func() {
				if p.client() != c {
					return
				}
				p.apply(c, tree)
				if tree.Tag == "trees" {
					p.mu.Lock()
					if p.control == c {
						p.finishSnapshotLocked()
					}
					p.mu.Unlock()
				}
			})
		}
	})
	c.Close()
	p.mu.Lock()
	current := p.control == c
	if current {
		p.control = nil
		p.finishSnapshotLocked()
	}
	p.mu.Unlock()
	if !current || p.d.stopping() || p.d.life.Context().Err() != nil {
		return
	}
	p.d.logf("lost app-server status at %s: %v", p.socket, err)
	for _, l := range p.sessions() {
		if _, err := p.ensure(p.d.life.Context(), l, nil, true); err != nil {
			l.logf("reconnecting: %v", err)
		}
		break
	}
}

func (p *serverConnection) statusWatchParams() any {
	after := map[string]int64{}
	numbered := false
	for id, l := range p.sessions() {
		if !l.tracksCompletions() {
			continue
		}
		numbered = true
		if conversation := p.d.linkConversation(id); conversation != "" {
			after[conversation] = p.d.store.SessionRuntimeRecords(id).ServerCompletion
		}
	}
	if !numbered {
		return nil
	}
	return map[string]any{"after": after}
}

func (p *serverConnection) apply(c *effectrpc.Client, tree serverTree) {
	if tree.Tag == "trees" {
		p.busy = make(map[string]string, len(tree.Trees))
		for _, t := range tree.Trees {
			p.busy[t.Conversation] = t.Status
		}
		var last map[string]*serverEnded
		for id, l := range p.sessions() {
			conversation := p.d.linkConversation(id)
			if status, ok := p.busy[conversation]; ok {
				l.observe(id, status, nil)
				continue
			}
			if !l.settlesOnServer(id) {
				continue
			}
			if last == nil {
				last = l.lastRuns(c)
			}
			l.observe(id, "idle", l.unseen(id, last[conversation]))
		}
		return
	}
	if tree.Ended != nil && tree.Status != "idle" {
		p.ended[tree.Conversation] = *tree.Ended
		return
	}
	ended := tree.Ended
	if remembered, ok := p.ended[tree.Conversation]; ok && tree.Status == "idle" {
		if ended == nil {
			ended = &remembered
		}
		delete(p.ended, tree.Conversation)
	}
	if tree.Status == "idle" {
		delete(p.busy, tree.Conversation)
	} else {
		if p.busy == nil {
			p.busy = make(map[string]string)
		}
		p.busy[tree.Conversation] = tree.Status
	}
	for id, l := range p.sessions() {
		if p.d.linkConversation(id) != tree.Conversation {
			continue
		}
		ended = l.unseen(id, ended)
		if ended != nil && tree.Status == "idle" {
			p.d.sessionMonitor(id).RecordRunAnswer(ended.Said)
		}
		l.observe(id, tree.Status, ended)
	}
}

func (p *serverConnection) reconcile(l *serverLink, ids []protocol.SessionID) {
	p.mu.Lock()
	c, ready := p.control, p.ready
	p.mu.Unlock()
	if c == nil || ready == nil {
		return
	}
	select {
	case <-ready:
	case <-p.d.life.Done():
		return
	}
	p.events.run(p.d, func() {
		if p.client() != c {
			return
		}
		var last map[string]*serverEnded
		for _, id := range ids {
			conversation := p.d.linkConversation(id)
			if status, busy := p.busy[conversation]; busy {
				l.observe(id, status, nil)
				continue
			}
			if last == nil {
				last = l.lastRuns(c)
			}
			ended := l.unseen(id, last[conversation])
			if ended != nil || !l.remembersItsLastRun(id) {
				l.observe(id, "idle", ended)
			}
		}
	})
}

func (d *Daemon) appServerProcess(ctx context.Context, terminal harness.TerminalID) (ptybackend.SessionInfo, bool) {
	if !strings.HasSuffix(string(terminal), "-server") {
		return ptybackend.SessionInfo{}, false
	}
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return ptybackend.SessionInfo{}, false
	}
	info, err := provider.SessionInfo(ctx, terminal)
	return info, err == nil && info.Agent == string(terminal)
}
