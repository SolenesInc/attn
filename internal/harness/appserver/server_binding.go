package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/setting"
)

type serverBinding struct {
	plugin         string
	enabled        func() bool
	displayName    func() string
	declaration    atomic.Pointer[Declaration]
	agent          string
	name           string
	kind           string
	terminal       harness.TerminalID
	conversationOf func(harness.TerminalID) (string, bool)
}

func newServerBinding(b Binding) *serverBinding {
	agent := b.Agent
	s := &serverBinding{agent: agent, name: agent, enabled: b.Enabled, displayName: b.DisplayName, kind: agent + "-server", terminal: harness.TerminalID(agent + "-server"), plugin: b.Plugin,
		conversationOf: func(t harness.TerminalID) (string, bool) {
			conversation, ok := strings.CutPrefix(string(t), "server:"+agent+":")
			return conversation, ok && conversation != ""
		},
	}
	s.declaration.Store(b.Declaration)
	return s
}

func (l *Link) home() string {
	env := append(os.Environ(), l.c.host.LoginEnv()...)
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

func (l *Link) homeEnv() string {
	return l.b.declaration.Load().HomeEnv + "=" + l.home()
}

func (l *Link) executable(req *harness.LaunchRequest) string {
	if p := l.b.declaration.Load(); p != nil {
		if req != nil {
			if configured := configuredExecutable(req); configured != "" {
				return configured
			}
		}
		if configured := strings.TrimSpace(l.c.host.Setting(setting.Executable.For(strings.TrimSpace(strings.ToLower(l.b.agent))))); configured != "" {
			return configured
		}
		if override := l.environmentValue(p.ExecutableEnv); override != "" {
			return override
		}
		return p.Executable
	}
	return ""
}

func (l *Link) environmentValue(key string) string {
	if key == "" {
		return ""
	}
	env := append(os.Environ(), l.c.host.LoginEnv()...)
	for i := len(env) - 1; i >= 0; i-- {
		if name, value, ok := strings.Cut(env[i], "="); ok && name == key {
			return value
		}
	}
	return ""
}

func (l *Link) socketPath() string {
	if p := l.b.declaration.Load(); p != nil {
		return filepath.Join(l.home(), p.SocketName)
	}
	return ""
}

func (l *Link) connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, effectrpc.ServerInfo, error) {
	p := l.b.declaration.Load()
	if p == nil {
		return nil, effectrpc.ServerInfo{}, &effectrpc.Error{Server: l.b.agent, Method: "initialize", Message: "server binding unavailable until plugin " + l.b.plugin + " registers"}
	}
	c, err := effectrpc.Dial(ctx, socket, l.serverName(p))
	if err != nil {
		return nil, effectrpc.ServerInfo{}, err
	}
	var hello effectrpc.ServerInfo
	err = c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": ProtocolVersion}, &hello)
	if err == nil {
		err = l.validateServerInfo(hello, socket, c.PeerPID())
	}
	if err != nil {
		c.Close()
		return nil, hello, err
	}
	return c, hello, nil
}

func (l *Link) callerConversationEnv() string {
	if p := l.b.declaration.Load(); p != nil {
		return p.CallerConversationEnv
	}
	return ""
}

func (l *Link) callerConversationHeader() string {
	if p := l.b.declaration.Load(); p != nil {
		return p.CallerConversationHeader
	}
	return ""
}

type serverHandshakeRefusal struct{ refusal *effectrpc.Error }

func (r *serverHandshakeRefusal) Error() string { return r.refusal.Error() }

func (r *serverHandshakeRefusal) Unwrap() error { return r.refusal }

func refuseHandshake(server, message string) error {
	return &serverHandshakeRefusal{refusal: &effectrpc.Error{Server: server, Method: "initialize", Message: message}}
}

func (l *Link) validateServerInfo(hello effectrpc.ServerInfo, socket string, pid int) error {
	p := l.b.declaration.Load()
	if p == nil {
		return fmt.Errorf("server binding unavailable until plugin %s registers", l.b.plugin)
	}
	want, server := p.Initialize, l.serverName(p)
	if want.Server != "" && hello.Server != want.Server {
		return refuseHandshake(server, fmt.Sprintf("server at %s is %q %s; binding requires identity %q", socket, hello.Server, hello.Version, want.Server))
	}
	if hello.Protocol == 1 {
		return refuseHandshake(server, l.protocolOneRefusal(p, socket, pid))
	}
	if hello.Protocol != ProtocolVersion {
		return refuseHandshake(server, fmt.Sprintf("server at %s is %q %s speaking protocol %d; binding requires protocol %d", socket, hello.Server, hello.Version, hello.Protocol, ProtocolVersion))
	}
	return nil
}

func (l *Link) serverName(p *Declaration) string {
	if p.Initialize.Server != "" {
		return p.Initialize.Server
	}
	return l.b.agent
}

func (l *Link) displayName() string {
	if l.b.displayName != nil {
		if name := l.b.displayName(); name != "" {
			return name
		}
	}
	return l.b.agent
}

func configuredExecutable(req *harness.LaunchRequest) string {
	if !req.Unattended.IsZero() {
		return req.Unattended.Executable
	}
	return req.Executable
}

func (l *Link) protocolOneRefusal(p *Declaration, socket string, pid int) string {
	name := l.displayName()
	process := "that server process"
	if pid > 0 {
		process = fmt.Sprintf("pid %d", pid)
	}
	command := strings.Join(append([]string{l.executable(nil)}, p.ServeArgs...), " ")
	return fmt.Sprintf("the %s server at %s speaks attn-app-server protocol 1, and this attn speaks only protocol %d. Stop %s (%s, %s) and update %s if it is still on protocol 1; attn starts a new one on next use", name, socket, ProtocolVersion, process, command, l.homeEnv(), name)
}
