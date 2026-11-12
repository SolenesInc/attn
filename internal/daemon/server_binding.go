package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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

func (d *Daemon) nisse() *serverLink {
	d.nisseOnce.Do(func() { d.nisseState = newServerLink(d, nisseBinding) })
	return d.nisseState
}

func (d *Daemon) sigma() *serverLink {
	d.sigmaOnce.Do(func() { d.sigmaState = newServerLink(d, sigmaBinding) })
	return d.sigmaState
}

func appServerBinding(agent string) *serverBinding {
	for _, b := range []*serverBinding{nisseBinding, sigmaBinding} {
		if b.agent == agent {
			return b
		}
	}
	return nil
}

func (d *Daemon) serverLinkFor(agent string) *serverLink {
	switch appServerBinding(agent) {
	case nisseBinding:
		return d.nisse()
	case sigmaBinding:
		return d.sigma()
	}
	return nil
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
	if home := strings.TrimSpace(lookup(l.b.homeVariable)); home != "" {
		return home
	}
	return filepath.Join(lookup("HOME"), l.b.homeDir)
}

func (l *serverLink) executable(opts *ptybackend.SpawnOptions) string {
	driver := agentdriver.MustGet(l.b.agent)
	if opts != nil {
		if configured := configuredLaunchExecutable(driver, opts); configured != "" {
			return driver.ResolveExecutable(configured)
		}
	}
	return driver.ResolveExecutable(l.d.setting(executableSetting(l.b.agent)))
}
