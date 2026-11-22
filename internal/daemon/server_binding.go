package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type serverBinding struct {
	plugin         string
	declaration    atomic.Pointer[agentdriver.ServerDeclaration]
	agent          string
	name           string
	kind           string
	terminal       harness.TerminalID
	conversationOf func(harness.TerminalID) (string, bool)
}

func newServerBinding(agent, plugin string, declaration *agentdriver.ServerDeclaration) *serverBinding {
	b := &serverBinding{agent: agent, name: agent, kind: agent + "-server", terminal: harness.TerminalID(agent + "-server"), plugin: plugin,
		conversationOf: func(t harness.TerminalID) (string, bool) {
			conversation, ok := strings.CutPrefix(string(t), "server:"+agent+":")
			return conversation, ok && conversation != ""
		},
	}
	b.declaration.Store(declaration)
	return b
}

func (d *Daemon) linkBuiltInServers() {
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	if d.serverLinks == nil {
		d.serverLinks = make(map[string]*serverLink)
	}
	for _, name := range agentdriver.List() {
		if declaration, ok := agentdriver.DeclaredServer(agentdriver.Get(name)); ok {
			d.serverLinks[name] = newServerLink(d, newServerBinding(name, "", &declaration))
		}
	}
}

var serverEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "ATTN_CODEX_SHARED_PROFILE=", "CODEX_THREAD_ID=", "BUN_GC_TIMER_DISABLE=1"}

func (d *Daemon) serverTerminalOf(agent string) func(string) harness.TerminalID {
	l := d.serverLinkFor(agent)
	if l == nil {
		return nil
	}
	return l.serverTerminal
}

func (d *Daemon) recoverServer(ctx context.Context, agent string, point harness.RecoverPoint) {
	l := d.serverLinkFor(agent)
	if l == nil {
		return
	}
	switch point {
	case harness.RecoverProcesses:
		l.recoverProcesses(ctx)
	case harness.RecoverViews:
		l.recoverViews(ctx)
	}
}

func (d *Daemon) releaseServer(agent string) {
	if l := d.serverLinkFor(agent); l != nil {
		l.profileReleased("")
	}
}

func (d *Daemon) admitToServer(agent string, ref harness.ConversationRef) error {
	if l := d.serverLinkFor(agent); l != nil && ref.Lifetime == harness.InServer {
		return l.admit(ref.Profile, ref.ID)
	}
	return nil
}

func (d *Daemon) serverLinkFor(agent string) *serverLink {
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	return d.serverLinks[agent]
}

func (d *Daemon) appServerLinks() []*serverLink {
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	links := make([]*serverLink, 0, len(d.serverLinks))
	for _, l := range d.serverLinks {
		links = append(links, l)
	}
	sort.Slice(links, func(i, j int) bool { return links[i].b.agent < links[j].b.agent })
	return links
}

func (l *serverLink) home() string {
	env := pty.MergeEnvironment(os.Environ(), l.d.latestLoginEnv())
	lookup := func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if name, value, ok := strings.Cut(env[i], "="); ok && name == key {
				return value
			}
		}
		return ""
	}
	var variable, dir string
	if p := l.b.declaration.Load(); p != nil {
		variable, dir = p.HomeEnv, p.DefaultHome
	}
	if home := strings.TrimSpace(lookup(variable)); home != "" {
		return home
	}
	return filepath.Join(lookup("HOME"), dir)
}

func (l *serverLink) homeEnv() string {
	return l.b.declaration.Load().HomeEnv + "=" + l.home()
}

func (l *serverLink) executable(opts *ptybackend.SpawnOptions) string {
	if p := l.b.declaration.Load(); p != nil {
		if opts != nil {
			if configured := configuredLaunchExecutable(nil, opts); configured != "" {
				return configured
			}
		}
		if configured := strings.TrimSpace(l.d.setting(executableSetting(l.b.agent))); configured != "" {
			return configured
		}
		if override := l.environmentValue(p.ExecutableEnv); override != "" {
			return override
		}
		return p.Executable
	}
	return ""
}

func (l *serverLink) environmentValue(key string) string {
	if key == "" {
		return ""
	}
	env := pty.MergeEnvironment(os.Environ(), l.d.latestLoginEnv())
	for i := len(env) - 1; i >= 0; i-- {
		if name, value, ok := strings.Cut(env[i], "="); ok && name == key {
			return value
		}
	}
	return ""
}

func (l *serverLink) socketPath() string {
	if p := l.b.declaration.Load(); p != nil {
		return filepath.Join(l.home(), p.SocketName)
	}
	return ""
}

func (l *serverLink) connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, effectrpc.ServerInfo, error) {
	p := l.b.declaration.Load()
	if p == nil {
		return nil, effectrpc.ServerInfo{}, &effectrpc.Error{Server: l.b.agent, Method: "initialize", Message: "server binding unavailable until plugin " + l.b.plugin + " registers"}
	}
	c, err := effectrpc.Dial(ctx, socket, p.Initialize.Server)
	if err != nil {
		return nil, effectrpc.ServerInfo{}, err
	}
	var hello effectrpc.ServerInfo
	err = c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": p.Initialize.Protocol.Requested()}, &hello)
	if err == nil {
		err = l.validateServerInfo(hello, socket)
	}
	if err != nil {
		c.Close()
		return nil, hello, err
	}
	return c, hello, nil
}

func (l *serverLink) callerConversationEnv() string {
	if p := l.b.declaration.Load(); p != nil {
		return p.CallerConversationEnv
	}
	return ""
}

func (l *serverLink) callerConversationHeader() string {
	if p := l.b.declaration.Load(); p != nil {
		return p.CallerConversationHeader
	}
	return ""
}

func (l *serverLink) validateServerInfo(hello effectrpc.ServerInfo, socket string) error {
	p := l.b.declaration.Load()
	if p == nil {
		return fmt.Errorf("server binding unavailable until plugin %s registers", l.b.plugin)
	}
	want := p.Initialize
	if (want.Server != "" && hello.Server != want.Server) || (want.Protocol.Exact > 0 && hello.Protocol != want.Protocol.Exact) || hello.Protocol < want.Protocol.Requested() {
		return &effectrpc.Error{Server: want.Server, Method: "initialize", Message: fmt.Sprintf("server at %s is %q %s speaking protocol %d; binding requires identity %q and protocol %+v", socket, hello.Server, hello.Version, hello.Protocol, want.Server, want.Protocol)}
	}
	return nil
}
