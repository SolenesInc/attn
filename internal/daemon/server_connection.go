package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
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
	tools       *serverToolsRegistration
	feeds       map[protocol.SessionID]*serverFeed
}

type serverToolsRegistration struct {
	client *effectrpc.Client
	drop   context.CancelFunc
	live   bool
}

func (l *serverLink) serverVersion() string {
	if l == nil {
		return ""
	}
	p, err := l.connection()
	if err != nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Version
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
		if info.Protocol > 0 && l.validateServerInfo(info, p.socket, 0) != nil {
			continue
		}
		peers = append(peers, l)
	}
	return peers
}

func (p *serverConnection) harnesses() string {
	var names []string
	for _, l := range p.peers() {
		names = append(names, l.b.agent)
	}
	return strings.Join(names, " or ")
}

func (p *serverConnection) sessions() map[protocol.SessionID]*serverLink {
	sessions := make(map[protocol.SessionID]*serverLink)
	for _, l := range p.peers() {
		for _, id := range l.sessions() {
			sessions[id] = l
		}
	}
	return sessions
}

func (p *serverConnection) toolsCaller(conversation string) protocol.SessionID {
	for _, peer := range p.peers() {
		if session := peer.holder(conversation); session != "" {
			return session
		}
	}
	return ""
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
		if err := l.validateServerInfo(info, p.socket, 0); err != nil {
			return nil, err
		}
		p.registerAgentToolsLocked(c)
		return c, nil
	}
	deadline := time.Now().Add(serverStartLimit)
	started := false
	for {
		attempt, cancel := context.WithTimeout(ctx, serverCallLimit)
		c, info, err := l.connect(attempt, p.socket, "attn", buildinfo.Version)
		cancel()
		if err == nil {
			if !started {
				l.d.adoptAdapterTerminal(ctx, appServerTerminalOwner, l.b.terminal)
			}
			p.hold(c, info)
			p.registerAgentToolsLocked(c)
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
	p.events.run(p.d, p.closeFeeds)
	if !p.d.life.Go("appServerStatus", func() { p.watch(c) }) {
		c.Close()
	}
}

func (p *serverConnection) agentTools() *agentdriver.AgentTools {
	tools := p.d.agentToolsFor(agentCredential{kind: credentialServer, name: p.socket})
	if tools == nil || p.callerConversationHeader() == "" {
		return nil
	}
	return tools
}

func (p *serverConnection) callerConversationHeader() string {
	for _, l := range p.peers() {
		if header := l.callerConversationHeader(); header != "" {
			return header
		}
	}
	return ""
}

func (p *serverConnection) registerAgentToolsLocked(c *effectrpc.Client) {
	p.mu.Lock()
	reg := p.tools
	p.mu.Unlock()
	if c != nil && (reg == nil || reg.client != c) {
		p.registerAgentTools(c)
	}
}

func (p *serverConnection) reconcileAgentTools() {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	wanted := p.agentTools() != nil
	p.mu.Lock()
	c, reg := p.control, p.tools
	if reg != nil && (reg.client != c || !wanted) {
		reg.drop()
		p.tools = nil
	}
	p.mu.Unlock()
	p.registerAgentToolsLocked(c)
}

func (p *serverConnection) registerAgentTools(c *effectrpc.Client) {
	tools := p.agentTools()
	if tools == nil {
		return
	}
	ctx, drop := context.WithCancel(p.d.life.Context())
	reg := &serverToolsRegistration{client: c, drop: drop}
	p.mu.Lock()
	if p.control != c {
		p.mu.Unlock()
		drop()
		return
	}
	if p.tools != nil {
		p.tools.drop()
	}
	p.tools = reg
	p.mu.Unlock()
	answer := make(chan error, 1)
	params := map[string]any{"name": agentToolsCatalog().Name, "url": tools.URL, "headers": map[string]string{"Authorization": "Bearer " + tools.Credential}}
	if !p.d.life.Go("serverAgentTools", func() {
		acknowledged := false
		err := c.Stream(ctx, "mcp/register", params, func(json.RawMessage) {
			p.mu.Lock()
			reg.live = p.tools == reg
			p.mu.Unlock()
			if !acknowledged {
				acknowledged = true
				answer <- nil
			}
		})
		p.mu.Lock()
		reg.live = false
		p.mu.Unlock()
		if !acknowledged {
			answer <- err
		} else if ctx.Err() == nil {
			p.d.logf("the server at %s ended attn's MCP server registration: %v; its agents use the attn CLI until attn connects again", p.socket, err)
		}
	}) {
		drop()
		return
	}
	wait, stop := context.WithTimeout(ctx, serverCallLimit)
	defer stop()
	select {
	case err := <-answer:
		if err != nil {
			p.d.logf("the server at %s refused attn's MCP server (mcp/register): %v; its agents use the attn CLI", p.socket, err)
		}
	case <-wait.Done():
		if ctx.Err() != nil {
			return
		}
		p.d.logf("the server at %s did not answer mcp/register within %s; its agents get attn's tools once it does", p.socket, serverCallLimit)
	}
}

func (p *serverConnection) agentToolsRegistered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tools != nil && p.tools.live && p.tools.client == p.control
}

func (d *Daemon) reconcileAgentTools() {
	d.serverLinksMu.Lock()
	connections := make([]*serverConnection, 0, len(d.serverConnections))
	for _, p := range d.serverConnections {
		connections = append(connections, p)
	}
	d.serverLinksMu.Unlock()
	for _, p := range connections {
		p.reconcileAgentTools()
	}
}

func (d *Daemon) serverConnectionAt(socket string) *serverConnection {
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	return d.serverConnections[socket]
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
		if conversation := p.d.serverConversation(id); conversation != "" {
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
			conversation := p.d.serverConversation(id)
			if status, ok := p.busy[conversation]; ok {
				l.observe(id, sessionmonitor.StateSourceServerReport, status, nil)
				continue
			}
			if !l.settlesOnServer(id) {
				continue
			}
			if last == nil {
				last = l.lastRuns(c)
			}
			l.observeQuiet(id, l.unseen(id, last[conversation]))
		}
		p.openFeeds(c, nil, false)
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
	if tree.Status != "idle" {
		p.openFeeds(c, map[string]bool{tree.Conversation: true}, true)
	}
	for id, l := range p.sessions() {
		if p.d.serverConversation(id) != tree.Conversation {
			continue
		}
		ended = l.unseen(id, ended)
		l.observe(id, sessionmonitor.StateSourceServerReport, tree.Status, ended)
	}
}

func (p *serverConnection) reconcile(l *serverLink, ids []protocol.SessionID, launching bool) {
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
		conversations := make(map[string]bool, len(ids))
		for _, id := range ids {
			conversation := p.d.serverConversation(id)
			conversations[conversation] = true
			if status, busy := p.busy[conversation]; busy {
				l.observe(id, sessionmonitor.StateSourceServerReport, status, nil)
				continue
			}
			if last == nil {
				last = l.lastRuns(c)
			}
			ended := l.unseen(id, last[conversation])
			if ended != nil || !l.remembersItsLastRun(id) {
				l.observeQuiet(id, ended)
			}
		}
		p.openFeeds(c, conversations, launching)
	})
}
