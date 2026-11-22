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

func (d *Daemon) planOnServer(agent, plugin string, req harness.LaunchRequest) harness.Planned {
	l := d.serverLinkFor(agent)
	if l == nil || plugin != "" && l.b.plugin != plugin {
		return harness.Planned{Lifetime: harness.InTerminal}
	}
	return planOn(l, req)
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
	c, err := effectrpc.Dial(ctx, socket, l.serverName(p))
	if err != nil {
		return nil, effectrpc.ServerInfo{}, err
	}
	var hello effectrpc.ServerInfo
	err = c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": agentdriver.ServerProtocolVersion}, &hello)
	if err == nil {
		err = l.validateServerInfo(hello, socket, c.PeerPID())
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

func (l *serverLink) validateServerInfo(hello effectrpc.ServerInfo, socket string, pid int) error {
	p := l.b.declaration.Load()
	if p == nil {
		return fmt.Errorf("server binding unavailable until plugin %s registers", l.b.plugin)
	}
	want, server := p.Initialize, l.serverName(p)
	if want.Server != "" && hello.Server != want.Server {
		return &effectrpc.Error{Server: server, Method: "initialize", Message: fmt.Sprintf("server at %s is %q %s; binding requires identity %q", socket, hello.Server, hello.Version, want.Server)}
	}
	if hello.Protocol == 1 {
		return &effectrpc.Error{Server: server, Method: "initialize", Message: l.protocolOneRefusal(p, socket, pid)}
	}
	if hello.Protocol != agentdriver.ServerProtocolVersion {
		return &effectrpc.Error{Server: server, Method: "initialize", Message: fmt.Sprintf("server at %s is %q %s speaking protocol %d; binding requires protocol %d", socket, hello.Server, hello.Version, hello.Protocol, agentdriver.ServerProtocolVersion)}
	}
	return nil
}

func (l *serverLink) serverName(p *agentdriver.ServerDeclaration) string {
	if p.Initialize.Server != "" {
		return p.Initialize.Server
	}
	return l.b.agent
}

func (l *serverLink) displayName() string {
	if name := l.d.factsOrNone(l.b.agent).DisplayName; name != "" {
		return name
	}
	return l.b.agent
}

func (l *serverLink) protocolOneRefusal(p *agentdriver.ServerDeclaration, socket string, pid int) string {
	name := l.displayName()
	process := "that server process"
	if pid > 0 {
		process = fmt.Sprintf("pid %d", pid)
	}
	command := strings.Join(append([]string{l.executable(nil)}, p.ServeArgs...), " ")
	return fmt.Sprintf("the %s server at %s speaks attn-app-server protocol 1, and this attn speaks only protocol %d. Stop %s (%s, %s) and update %s if it is still on protocol 1; attn starts a new one on next use", name, socket, agentdriver.ServerProtocolVersion, process, command, l.homeEnv(), name)
}
