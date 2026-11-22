package appserver

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
)

type serverConnection struct {
	c           *Client
	socket      string
	ensureMu    sync.Mutex
	uses        int
	mu          sync.Mutex
	control     *effectrpc.Client
	info        effectrpc.ServerInfo
	ready       chan struct{}
	readyClosed bool
	terminal    harness.TerminalID
	events      appServerEvents
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

func (l *Link) Version() string {
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

func (l *Link) connection() (*serverConnection, error) {
	socket, err := config.CanonicalRuntimePath(l.socketPath())
	if err != nil {
		return nil, err
	}
	if socket == "" {
		return nil, &effectrpc.Error{Server: l.b.agent, Method: "initialize", Message: "server binding unavailable until plugin " + l.b.plugin + " registers"}
	}
	cl := l.c
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if p := cl.connections[socket]; p != nil {
		return p, nil
	}
	p := &serverConnection{c: cl, socket: socket, ended: make(map[string]serverEnded)}
	cl.connections[socket] = p
	cl.host.Life().Go("appServerStop", func() {
		<-cl.host.Life().Context().Done()
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

func (p *serverConnection) peers() []*Link {
	p.mu.Lock()
	info := p.info
	p.mu.Unlock()
	var peers []*Link
	for _, l := range p.c.Links() {
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

func (p *serverConnection) sessions() map[protocol.SessionID]*Link {
	sessions := make(map[protocol.SessionID]*Link)
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

func (p *serverConnection) ensure(ctx context.Context, l *Link, req *harness.LaunchRequest, start bool) (*effectrpc.Client, error) {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	return p.ensureLocked(ctx, l, req, start)
}

func (p *serverConnection) ensureLocked(ctx context.Context, l *Link, req *harness.LaunchRequest, start bool) (*effectrpc.Client, error) {
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
				l.c.host.Terminals().Adopt(ctx, l.b.terminal)
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
			if err := l.startServer(ctx, req, terminal); err != nil {
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

func (p *serverConnection) borrow(ctx context.Context, l *Link, start bool) (*effectrpc.Client, func(), error) {
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
		p.c.host.Logf("let go of app-server at %s; no session uses it", p.socket)
	}
}

func (p *serverConnection) hold(c *effectrpc.Client, info effectrpc.ServerInfo) {
	p.mu.Lock()
	p.control, p.info = c, info
	p.ready = make(chan struct{})
	p.readyClosed = false
	p.mu.Unlock()
	p.events.run(p.c, p.closeFeeds)
	if !p.c.host.Life().Go("appServerStatus", func() { p.watch(c) }) {
		c.Close()
	}
}

func (p *serverConnection) agentTools() *harness.AgentTools {
	tools, ok := p.c.host.ServerTools(p.socket)
	if !ok || p.callerConversationHeader() == "" {
		return nil
	}
	return &tools
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
	ctx, drop := context.WithCancel(p.c.host.Life().Context())
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
	params := map[string]any{"name": tools.Name, "url": tools.URL, "headers": map[string]string{"Authorization": "Bearer " + tools.Credential}}
	if !p.c.host.Life().Go("serverAgentTools", func() {
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
			p.c.host.Logf("the server at %s ended attn's MCP server registration: %v; its agents use the attn CLI until attn connects again", p.socket, err)
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
			p.c.host.Logf("the server at %s refused attn's MCP server (mcp/register): %v; its agents use the attn CLI", p.socket, err)
		}
	case <-wait.Done():
		if ctx.Err() != nil {
			return
		}
		p.c.host.Logf("the server at %s did not answer mcp/register within %s; its agents get attn's tools once it does", p.socket, serverCallLimit)
	}
}

func (p *serverConnection) agentToolsRegistered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tools != nil && p.tools.live && p.tools.client == p.control
}

func (c *Client) reconcileAgentTools() {
	c.mu.Lock()
	connections := make([]*serverConnection, 0, len(c.connections))
	for _, p := range c.connections {
		connections = append(connections, p)
	}
	c.mu.Unlock()
	for _, p := range connections {
		p.reconcileAgentTools()
	}
}

func (c *Client) serverConnectionAt(socket string) *serverConnection {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connections[socket]
}

func (p *serverConnection) finishSnapshotLocked() {
	if p.ready != nil && !p.readyClosed {
		close(p.ready)
		p.readyClosed = true
	}
}

func (p *serverConnection) watch(c *effectrpc.Client) {
	err := c.Stream(p.c.host.Life().Context(), "status/watch", p.statusWatchParams(), func(item json.RawMessage) {
		var tree serverTree
		if json.Unmarshal(item, &tree) == nil {
			p.events.run(p.c, func() {
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
	if !current || p.c.host.Life().Context().Err() != nil {
		return
	}
	p.c.host.Logf("lost app-server status at %s: %v", p.socket, err)
	for _, l := range p.sessions() {
		if _, err := p.ensure(p.c.host.Life().Context(), l, nil, true); err != nil {
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
		if conversation := p.c.host.Sessions().Conversation(id); conversation != "" {
			after[conversation] = p.c.host.Records().Feed(id).Completion
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
			conversation := p.c.host.Sessions().Conversation(id)
			if status, ok := p.busy[conversation]; ok {
				l.observe(id, false, status, nil)
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
		if p.c.host.Sessions().Conversation(id) != tree.Conversation {
			continue
		}
		ended = l.unseen(id, ended)
		l.observe(id, false, tree.Status, ended)
	}
}

func (p *serverConnection) reconcile(l *Link, ids []protocol.SessionID, launching bool) {
	p.mu.Lock()
	c, ready := p.control, p.ready
	p.mu.Unlock()
	if c == nil || ready == nil {
		return
	}
	select {
	case <-ready:
	case <-p.c.host.Life().Context().Done():
		return
	}
	p.events.run(p.c, func() {
		if p.client() != c {
			return
		}
		var last map[string]*serverEnded
		conversations := make(map[string]bool, len(ids))
		for _, id := range ids {
			conversation := p.c.host.Sessions().Conversation(id)
			conversations[conversation] = true
			if status, busy := p.busy[conversation]; busy {
				l.observe(id, false, status, nil)
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
