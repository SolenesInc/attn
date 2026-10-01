package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const codexServerRuntime = "attn-codex-server"
const codexViewGenerationPrefix = "codex-view:"
const SettingCodexSharedEnabled = "codex_shared_enabled"

type codexLaunchContext struct {
	CWD            string
	WorkspaceID    string
	Executable     string
	Model          string
	Effort         string
	Yolo           bool
	AutoApprove    bool
	ApprovalPolicy json.RawMessage
	Sandbox        string
	Reviewer       string
	Guidance       hooks.Launch
}

type codexRuntime struct {
	d               *Daemon
	mu              sync.Mutex
	serverMu        sync.Mutex
	exitMu          sync.Mutex
	serverExit      chan error
	serverID        string
	control         *codexshared.Client
	views           map[string]store.CodexView
	servers         map[string]*http.Server
	initialConsumed map[string]bool
	activeMu        sync.Mutex
	activeTurns     map[string]codexTurnState
}

func (d *Daemon) codexRuntime() *codexRuntime {
	d.codexOnce.Do(func() {
		d.codex = &codexRuntime{d: d, serverID: "codex", views: make(map[string]store.CodexView), servers: make(map[string]*http.Server), initialConsumed: make(map[string]bool), activeTurns: make(map[string]codexTurnState)}
	})
	return d.codex
}

func (r *codexRuntime) socket(runtimeID string) string {
	if runtimeID == "" {
		return filepath.Join(r.d.dataRoot, "cx", "server", "rpc.sock")
	}
	name := ""
	if runtimeID != "" {
		sum := sha256.Sum256([]byte(runtimeID))
		name = hex.EncodeToString(sum[:16])
	}
	return filepath.Join(r.d.dataRoot, "cx", name+".sock")
}

func (r *codexRuntime) ensureServer(ctx context.Context, launch codexLaunchContext) error {
	r.serverMu.Lock()
	defer r.serverMu.Unlock()
	if r.control != nil && r.control.Connected() {
		return nil
	}
	dir := filepath.Dir(r.socket(""))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	alive := false
	var exited <-chan error
	ids := r.d.ptyBackend.SessionIDs(ctx)
	for _, id := range ids {
		if id == codexServerRuntime {
			alive = true
			if provider, ok := r.d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
				info, err := provider.SessionInfo(ctx, id)
				alive = err == nil && info.Running
			}
		}
	}
	if !alive {
		if err := r.d.ptyBackend.Remove(ctx, codexServerRuntime); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return err
		}
		exitNotice := make(chan error, 1)
		exited = exitNotice
		r.exitMu.Lock()
		r.serverExit = exitNotice
		r.exitMu.Unlock()
		if err := os.Remove(r.socket("")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer watcher.Close()
		if err := watcher.Add(dir); err != nil {
			return err
		}
		executable := agentdriver.MustGet("codex").ResolveExecutable(launch.Executable)
		opts := ptybackend.SpawnOptions{ID: codexServerRuntime, Agent: "codex", CWD: dir, Cols: 80, Rows: 24, ExternalCommand: []string{executable, "app-server", "--listen", "unix://" + r.socket("")}, LoginShellEnv: r.d.cachedLoginShellEnv(), DaemonEnv: r.d.spawnRoutingEnv()}
		if err := r.d.ptyBackend.Spawn(ctx, opts); err != nil {
			return fmt.Errorf("start shared Codex server: %w", err)
		}
		for {
			if _, err := os.Stat(r.socket("")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-exited:
				return err
			case _, ok := <-watcher.Events:
				if !ok {
					return errors.New("codex socket watcher closed")
				}
			case err := <-watcher.Errors:
				return err
			}
		}
	}
	var control *codexshared.Client
	var err error
	for {
		control, err = codexshared.Connect(r.d.life.Context(), r.socket(""), r.observeNative)
		if !errors.Is(err, syscall.ECONNREFUSED) || alive {
			break
		}
		// Stock 0.159.3 socket-to-handshake was <=24ms over five starts; 100ms
		// matches worker startup polling. Retry only a refused, unopened transport.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case err := <-exited:
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
	if err != nil {
		return fmt.Errorf("connect shared Codex server: %w", err)
	}
	r.control = control
	r.d.logf("shared Codex control connected")
	owners, err := r.d.store.CodexOwners(r.serverID)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if owner.Archived || owner.NativeRootID == "" {
			continue
		}
		context, err := r.ownerContext(&owner)
		if err != nil {
			return err
		}
		params := map[string]any{"threadId": owner.NativeRootID}
		injectCodexOwner(params, owner.SessionID, r.d.socketPath, r.d.wrapperExecutable(), context)
		r.d.logf("restore shared Codex owner %s: beginning native resume", owner.SessionID)
		if _, err := r.resumeOwner(ctx, control, params); err != nil {
			r.d.logf("restore shared Codex owner %s: %v", owner.SessionID, err)
		}
	}
	r.d.logf("shared Codex owners restored")
	return nil
}

func (r *codexRuntime) recover() error {
	views, err := r.d.store.CodexViews()
	if err != nil {
		return err
	}
	owners, err := r.d.store.CodexOwners(r.serverID)
	if err != nil {
		return err
	}
	var owner *store.CodexOwner
	for i := range owners {
		if !owners[i].Archived {
			owner = &owners[i]
			break
		}
	}
	if owner == nil {
		return nil
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		return err
	}
	r.mu.Lock()
	err = r.ensureServer(r.d.life.Context(), launch)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	for _, v := range views {
		if err := r.addView(v); err != nil {
			return err
		}
		if provider, ok := r.d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
			info, err := provider.SessionInfo(r.d.life.Context(), v.RuntimeID)
			if err != nil || !info.Running {
				r.disconnectView(v.RuntimeID)
			} else if !info.TitleObservation.At.IsZero() {
				r.observeTitle(v.RuntimeID, info.TitleObservation)
			}
		}
	}
	return nil
}

func (r *codexRuntime) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, server := range r.servers {
		_ = server.Close()
	}
	if r.control != nil {
		r.control.Close()
	}
}

func (r *codexRuntime) noteServerExit(info ptybackend.ExitInfo) {
	r.exitMu.Lock()
	exited := r.serverExit
	r.exitMu.Unlock()
	if exited != nil {
		select {
		case exited <- fmt.Errorf("shared Codex server exited with code %d signal %s", info.ExitCode, info.Signal):
		default:
		}
	}
	r.d.life.Go("codexServerInterrupted", func() {
		r.activeMu.Lock()
		clear(r.activeTurns)
		r.activeMu.Unlock()
		owners, err := r.d.store.CodexOwners(r.serverID)
		if err != nil {
			return
		}
		for _, owner := range owners {
			if !owner.Archived {
				r.d.applyState(sessionStateChange{sessionID: owner.SessionID, state: string(protocol.SessionStateRecoverable), cause: startupRecovery{}, origin: stateOrigin{source: "codex", detail: "native server exited; interrupted input is not replayed"}})
			}
		}
	})
}
func (r *codexRuntime) hasRuntime(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.views[id]
	return ok
}
func (d *Daemon) sharedCodexOwner(id string) bool {
	owner, err := d.store.CodexOwner(id)
	return err == nil && owner != nil
}

func (d *Daemon) sharedCodexEvidence(launch codexLaunchContext) sessionstate.Evidence {
	return sessionstate.Evidence{ReviewerInLoop: launch.AutoApprove}
}
