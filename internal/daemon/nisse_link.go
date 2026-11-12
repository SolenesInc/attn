package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/nisse"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
)

const (
	nisseLinkKind                          = "nisse-server"
	nisseAgent                             = "nisse"
	nisseServerTerminal harness.TerminalID = "nisse-server"
)

var nisseServerEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT=", "ATTN_CODEX_SHARED_PROFILE=", "CODEX_THREAD_ID=", "BUN_GC_TIMER_DISABLE=1"}

type nisseServer struct{ d *Daemon }

func (d *Daemon) nisse() *serverLink {
	d.nisseOnce.Do(func() { d.nisseState = newServerLink(d, nisseServer{d: d}) })
	return d.nisseState
}

func (d *Daemon) nisseEnabled() bool {
	return d.on(setting.NisseEnabled)
}

func (d *Daemon) agentOffered(agent string) bool {
	return agent != nisseAgent || d.nisseEnabled()
}

func (nisseServer) kind() string { return nisseLinkKind }

func (nisseServer) agent() string { return nisseAgent }

func (nisseServer) name() string { return "Nisse" }

func (n nisseServer) enabled() bool { return n.d.nisseEnabled() }

func (n nisseServer) home() string {
	env := pty.MergeEnvironment(os.Environ(), n.d.latestLoginEnv())
	lookup := func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if name, value, ok := strings.Cut(env[i], "="); ok && name == key {
				return value
			}
		}
		return ""
	}
	if home := strings.TrimSpace(lookup("NISSE_HOME")); home != "" {
		return home
	}
	return filepath.Join(lookup("HOME"), ".nisse")
}

func (nisseServer) socket(home string) string { return nisse.Socket(home) }

func (nisseServer) connect(ctx context.Context, socket string) (*effectrpc.Client, error) {
	return nisse.Connect(ctx, socket, "attn", buildinfo.Version)
}

func (n nisseServer) executable(opts *ptybackend.SpawnOptions) string {
	driver := agentdriver.MustGet(nisseAgent)
	if opts != nil {
		if configured := configuredLaunchExecutable(driver, opts); configured != "" {
			return driver.ResolveExecutable(configured)
		}
	}
	return driver.ResolveExecutable(n.d.setting(executableSetting(nisseAgent)))
}

func (nisseServer) serve(executable, home string) ([]string, []string) {
	return []string{executable, "serve"}, append(append([]string(nil), nisseServerEnv...), "NISSE_HOME="+home)
}

func (nisseServer) terminal() harness.TerminalID { return nisseServerTerminal }

func (nisseServer) conversationOf(t harness.TerminalID) (string, bool) {
	return harness.ParseNisseConversationTerminal(t)
}

func (n nisseServer) sessionClosed(sessionID protocol.SessionID, conversation string) {
	if conversation == "" || n.d.store.OtherOpenSessionHolding(conversation, sessionID) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(n.d.life.Context(), serverCallLimit)
	defer cancel()
	c, err := n.connect(ctx, n.socket(n.home()))
	if err != nil {
		return
	}
	defer c.Close()
	if err := c.Call(ctx, "conversation/interrupt", map[string]any{"conversation": conversation}, nil); err != nil {
		n.d.logf("nisse: interrupting conversation %s of closed session %s: %v", conversation, sessionID, err)
	}
}
