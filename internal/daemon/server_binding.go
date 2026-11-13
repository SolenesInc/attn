package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type serverBinding struct {
	plugin         string
	declaration    atomic.Pointer[pluginServerDeclaration]
	agent          string
	name           string
	kind           string
	terminal       harness.TerminalID
	conversationOf func(harness.TerminalID) (string, bool)
}

var serverEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "ATTN_CODEX_SHARED_PROFILE=", "CODEX_THREAD_ID=", "BUN_GC_TIMER_DISABLE=1"}

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

func (d *Daemon) agentOffered(agent string) bool {
	l := d.serverLinkFor(agent)
	return l == nil || l.enabledForNewLaunches()
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
	return p.connect(ctx, socket, client, version)
}

func (l *serverLink) callerConversationEnv() string {
	if p := l.b.declaration.Load(); p != nil {
		return p.CallerConversationEnv
	}
	return ""
}

func (l *serverLink) validateServerInfo(hello effectrpc.ServerInfo, socket string) error {
	if p := l.b.declaration.Load(); p != nil {
		return p.validateServerInfo(hello, socket)
	}
	return fmt.Errorf("server binding unavailable until plugin %s registers", l.b.plugin)
}
