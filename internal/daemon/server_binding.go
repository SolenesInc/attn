package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/nisse"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/sigma"
)

type serverBinding struct {
	plugin            string
	declaration       atomic.Pointer[pluginServerDeclaration]
	agent             string
	name              string
	kind              string
	terminal          harness.TerminalID
	feature           *setting.Setting
	homeVariable      string
	homeDir           string
	socket            func(home string) string
	connect           func(ctx context.Context, socket, client, version string) (*effectrpc.Client, error)
	conversationOf    func(harness.TerminalID) (string, bool)
	tracksCompletions bool
	closeLeavesRun    bool
}

const (
	nisseAgent = "nisse"
	sigmaAgent = "sigma"
)

var nisseBinding = &serverBinding{
	agent:          nisseAgent,
	name:           "Nisse",
	kind:           "nisse-server",
	terminal:       "nisse-server",
	feature:        setting.NisseEnabled,
	homeVariable:   "NISSE_HOME",
	homeDir:        ".nisse",
	socket:         nisse.Socket,
	connect:        nisse.Connect,
	conversationOf: harness.ParseNisseConversationTerminal,
}

var sigmaBinding = &serverBinding{
	agent:             sigmaAgent,
	name:              "Sigma",
	kind:              "sigma-server",
	terminal:          "sigma-server",
	feature:           setting.SigmaEnabled,
	homeVariable:      "SIGMA_HOME",
	homeDir:           ".sigma",
	socket:            sigma.Socket,
	connect:           sigma.Connect,
	conversationOf:    harness.ParseSigmaConversationTerminal,
	tracksCompletions: true,
	closeLeavesRun:    true,
}

var serverEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "ATTN_CODEX_SHARED_PROFILE=", "CODEX_THREAD_ID=", "BUN_GC_TIMER_DISABLE=1"}

func appServerBinding(agent string) *serverBinding {
	for _, b := range []*serverBinding{nisseBinding, sigmaBinding} {
		if b.agent == agent {
			return b
		}
	}
	return nil
}

func (d *Daemon) serverLinkFor(agent string) *serverLink {
	d.serverLinksMu.Lock()
	defer d.serverLinksMu.Unlock()
	if l := d.serverLinks[agent]; l != nil {
		return l
	}
	b := appServerBinding(agent)
	if b == nil {
		return nil
	}
	if d.serverLinks == nil {
		d.serverLinks = make(map[string]*serverLink)
	}
	l := newServerLink(d, b)
	d.serverLinks[agent] = l
	return l
}

func (d *Daemon) appServerLinks() []*serverLink {
	for _, b := range []*serverBinding{nisseBinding, sigmaBinding} {
		d.serverLinkFor(b.agent)
	}
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
	variable, dir := l.b.homeVariable, l.b.homeDir
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
		if override := l.environmentValue(p.ExecutableEnv); override != "" {
			return override
		}
		return p.Executable
	}
	driver := agentdriver.MustGet(l.b.agent)
	if opts != nil {
		if configured := configuredLaunchExecutable(driver, opts); configured != "" {
			return driver.ResolveExecutable(configured)
		}
	}
	return driver.ResolveExecutable(l.d.setting(executableSetting(l.b.agent)))
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
	if l.b.socket == nil {
		return ""
	}
	return l.b.socket(l.home())
}

func (l *serverLink) connect(ctx context.Context, socket, client, version string) (*effectrpc.Client, error) {
	if l.b.plugin != "" {
		p := l.b.declaration.Load()
		if p == nil {
			return nil, &effectrpc.Error{Server: l.b.agent, Method: "initialize", Message: "server binding unavailable until plugin " + l.b.plugin + " registers"}
		}
		return p.connect(ctx, socket, client, version)
	}
	return l.b.connect(ctx, socket, client, version)
}
