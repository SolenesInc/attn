package ptybackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

type sharedHostControl struct {
	mu           sync.Mutex
	socketPath   string
	controlToken string
	conn         net.Conn
	enc          *json.Encoder
	dec          *json.Decoder
}

type sharedHostMonitor struct {
	socketPath   string
	controlToken string
	stop         chan struct{}
	done         chan struct{}
	stopOnce     sync.Once
	fallback     bool
	stopping     bool
}

func (b *WorkerBackend) callResultSharedOneShot(ctx context.Context, session *workerSession, method string, params, result any) error {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return err
	}
	reqID := b.nextReqID(method)
	if err := writeRequest(enc, reqID, method, params); err != nil {
		return err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return b.rpcError(session.SessionID, res.Error)
		}
		if result != nil {
			if err := json.Unmarshal(res.Result, result); err != nil {
				return fmt.Errorf("decode shared PTY host %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (b *WorkerBackend) callResultSharedPersistent(ctx context.Context, session *workerSession, method string, params, result any) (bool, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	control := b.sharedControl(session)
	control.mu.Lock()
	defer control.mu.Unlock()

	if err := b.ensureSharedControlLocked(rpcCtx, control); err != nil {
		return false, err
	}
	err := b.callResultOnSharedControlLocked(rpcCtx, control, session.SessionID, method, params, result)
	if err == nil || !isRetryablePersistentConnError(err) || rpcCtx.Err() != nil {
		return false, err
	}
	b.closeSharedControlLocked(control)
	if err := b.ensureSharedControlLocked(rpcCtx, control); err != nil {
		return true, err
	}
	return true, b.callResultOnSharedControlLocked(rpcCtx, control, session.SessionID, method, params, result)
}

func (b *WorkerBackend) sharedControl(session *workerSession) *sharedHostControl {
	key := filepath.Clean(session.SocketPath)
	b.sharedControlMu.Lock()
	defer b.sharedControlMu.Unlock()
	if control := b.sharedControls[key]; control != nil {
		return control
	}
	control := &sharedHostControl{socketPath: session.SocketPath, controlToken: session.ControlToken}
	b.sharedControls[key] = control
	return control
}

func (b *WorkerBackend) ensureSharedControlLocked(ctx context.Context, control *sharedHostControl) error {
	if control.conn != nil && control.enc != nil && control.dec != nil {
		return nil
	}
	host := &workerSession{SocketPath: control.socketPath, ControlToken: control.controlToken}
	conn, enc, dec, err := b.connectWithIdentity(ctx, host, b.cfg.DaemonInstanceID, control.controlToken)
	if err != nil {
		return err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return err
	}
	control.conn, control.enc, control.dec = conn, enc, dec
	return nil
}

func (b *WorkerBackend) callResultOnSharedControlLocked(ctx context.Context, control *sharedHostControl, sessionID, method string, params, result any) error {
	if control.conn == nil || control.enc == nil || control.dec == nil {
		return errors.New("shared PTY host control connection is not initialized")
	}
	conn := control.conn
	if err := applyConnDeadline(conn, ctx); err != nil {
		b.closeSharedControlLocked(control)
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	reqID := b.nextReqID(method)
	if err := writeRequestForSession(control.enc, reqID, method, sessionID, params); err != nil {
		b.closeSharedControlLocked(control)
		return err
	}
	for {
		frameType, res, _, err := readFrame(control.dec)
		if err != nil {
			b.closeSharedControlLocked(control)
			return err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return b.rpcError(sessionID, res.Error)
		}
		if result != nil {
			if err := json.Unmarshal(res.Result, result); err != nil {
				return fmt.Errorf("decode shared PTY host %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (b *WorkerBackend) closeSharedControlLocked(control *sharedHostControl) {
	if control.conn != nil {
		_ = control.conn.Close()
	}
	control.conn, control.enc, control.dec = nil, nil, nil
}

func (b *WorkerBackend) closeSharedControls() {
	b.sharedControlMu.Lock()
	controls := make([]*sharedHostControl, 0, len(b.sharedControls))
	for _, control := range b.sharedControls {
		controls = append(controls, control)
	}
	b.sharedControls = make(map[string]*sharedHostControl)
	b.sharedControlMu.Unlock()
	for _, control := range controls {
		control.mu.Lock()
		b.closeSharedControlLocked(control)
		control.mu.Unlock()
	}
}

func (b *WorkerBackend) closeSharedControl(socketPath string) {
	key := filepath.Clean(socketPath)
	b.sharedControlMu.Lock()
	control := b.sharedControls[key]
	delete(b.sharedControls, key)
	b.sharedControlMu.Unlock()
	if control == nil {
		return
	}
	control.mu.Lock()
	b.closeSharedControlLocked(control)
	control.mu.Unlock()
}

func (b *WorkerBackend) startSharedHostMonitor(session *workerSession) {
	key := filepath.Clean(session.SocketPath)
	b.sharedMonitorMu.Lock()
	if b.sharedStopping {
		b.sharedMonitorMu.Unlock()
		return
	}
	monitor := b.sharedMonitors[key]
	if monitor != nil {
		if monitor.stopping {
			b.sharedMonitorMu.Unlock()
			return
		}
		fallback := monitor.fallback
		b.sharedMonitorMu.Unlock()
		if fallback {
			b.startSessionMonitor(session)
			return
		}
		go b.syncSharedSessionLifecycle(session)
		return
	}
	monitor = &sharedHostMonitor{
		socketPath:   session.SocketPath,
		controlToken: session.ControlToken,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	b.sharedMonitors[key] = monitor
	b.sharedMonitorMu.Unlock()
	go b.serveSharedHostMonitor(monitor)
}

func (b *WorkerBackend) serveSharedHostMonitor(monitor *sharedHostMonitor) {
	defer func() {
		key := filepath.Clean(monitor.socketPath)
		b.sharedMonitorMu.Lock()
		if b.sharedMonitors[key] == monitor && !monitor.fallback {
			delete(b.sharedMonitors, key)
		}
		b.sharedMonitorMu.Unlock()
		close(monitor.done)
	}()

	var unreachableAt time.Time
	for {
		select {
		case <-monitor.stop:
			return
		default:
		}

		err := b.runSharedHostMonitor(monitor)
		if err == nil {
			return
		}
		if errors.Is(err, errLifecycleWatchUnsupported) || errors.Is(err, errLifecycleWatchHandshakeTimeout) {
			b.cfg.Logf("shared PTY host lifecycle stream unavailable at %s; falling back to per-session streams", monitor.socketPath)
			b.installSharedHostMonitorFallback(monitor)
			return
		}
		select {
		case <-monitor.stop:
			return
		default:
		}

		if b.dropSharedHostMonitorIfUnused(monitor) {
			return
		}
		sessions := b.sharedHostSessions(monitor.socketPath)
		b.cfg.Logf("shared PTY host lifecycle stream disconnected at %s: %v", monitor.socketPath, err)
		alive, probeErr := b.sharedHostLikelyAlive(monitor, sessions)
		if !alive {
			b.notifySharedHostLost(monitor.socketPath)
			return
		}
		if probeErr == nil {
			unreachableAt = time.Time{}
		} else if unreachableAt.IsZero() {
			unreachableAt = time.Now()
		} else if time.Since(unreachableAt) >= pollerUnreachableAfter {
			b.cfg.Logf("shared PTY host remained unreachable at %s: %v", monitor.socketPath, probeErr)
			b.notifySharedHostLost(monitor.socketPath)
			return
		}

		timer := time.NewTimer(monitorRetryInterval)
		select {
		case <-monitor.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (b *WorkerBackend) runSharedHostMonitor(monitor *sharedHostMonitor) error {
	callCtx, cancel := withDefaultRPCTimeout(context.Background())
	host := &workerSession{SocketPath: monitor.socketPath, ControlToken: monitor.controlToken}
	conn, enc, dec, err := b.connectWithIdentity(callCtx, host, b.cfg.DaemonInstanceID, monitor.controlToken)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()

	stopDone := make(chan struct{})
	go func() {
		select {
		case <-monitor.stop:
			_ = conn.Close()
		case <-stopDone:
		}
	}()
	defer close(stopDone)

	watchReqID := b.nextReqID("watch-all")
	if err := writeRequest(enc, watchReqID, ptyhost.MethodWatchAll, map[string]any{}); err != nil {
		return err
	}

	timedOut := make(chan struct{})
	handshakeTimer := time.AfterFunc(watchResponseTimeout, func() {
		close(timedOut)
		_ = conn.Close()
	})
	defer handshakeTimer.Stop()

	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			select {
			case <-monitor.stop:
				return nil
			default:
			}
			select {
			case <-timedOut:
				return errLifecycleWatchHandshakeTimeout
			default:
				return err
			}
		}
		if frameType != "res" || res.ID != watchReqID {
			continue
		}
		if !res.OK {
			if isLifecycleWatchUnsupported(res.Error) {
				return errLifecycleWatchUnsupported
			}
			return b.rpcError("", res.Error)
		}
		handshakeTimer.Stop()
		break
	}

	for {
		frameType, _, evt, err := readFrame(dec)
		if err != nil {
			select {
			case <-monitor.stop:
				return nil
			default:
				return err
			}
		}
		if frameType == "evt" {
			b.handleSharedLifecycleEvent(monitor.socketPath, evt)
		}
	}
}

func (b *WorkerBackend) installSharedHostMonitorFallback(monitor *sharedHostMonitor) {
	key := filepath.Clean(monitor.socketPath)
	b.sharedMonitorMu.Lock()
	if b.sharedStopping || monitor.stopping || b.sharedMonitors[key] != monitor {
		b.sharedMonitorMu.Unlock()
		return
	}
	monitor.fallback = true
	b.sharedMonitorMu.Unlock()
	for _, session := range b.sharedHostSessions(monitor.socketPath) {
		b.startSessionMonitor(session)
	}
}

func (b *WorkerBackend) closeSharedMonitors() {
	b.sharedMonitorMu.Lock()
	b.sharedStopping = true
	monitors := make([]*sharedHostMonitor, 0, len(b.sharedMonitors))
	for _, monitor := range b.sharedMonitors {
		monitor.stopping = true
		monitors = append(monitors, monitor)
	}
	b.sharedMonitors = make(map[string]*sharedHostMonitor)
	b.sharedMonitorMu.Unlock()
	for _, monitor := range monitors {
		monitor.stopOnce.Do(func() { close(monitor.stop) })
		<-monitor.done
	}
}

func (b *WorkerBackend) dropSharedHostMonitorIfUnused(monitor *sharedHostMonitor) bool {
	key := filepath.Clean(monitor.socketPath)
	b.sharedMonitorMu.Lock()
	defer b.sharedMonitorMu.Unlock()
	if b.sharedStopping || monitor.stopping || b.sharedMonitors[key] != monitor {
		return true
	}

	b.mu.RLock()
	hasSession := false
	for _, session := range b.sessions {
		if filepath.Clean(session.SocketPath) == key {
			hasSession = true
			break
		}
	}
	b.mu.RUnlock()
	if hasSession {
		return false
	}
	monitor.stopping = true
	delete(b.sharedMonitors, key)
	return true
}

func (b *WorkerBackend) sharedHostSessions(socketPath string) []*workerSession {
	key := filepath.Clean(socketPath)
	b.mu.RLock()
	defer b.mu.RUnlock()
	sessions := make([]*workerSession, 0)
	for _, session := range b.sessions {
		if filepath.Clean(session.SocketPath) == key {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (b *WorkerBackend) sharedHostLikelyAlive(monitor *sharedHostMonitor, sessions []*workerSession) (bool, error) {
	for _, session := range sessions {
		if session.WorkerPID > 0 && !pidAlive(session.WorkerPID) {
			return false, nil
		}
	}
	if _, err := os.Stat(monitor.socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
	defer cancel()
	err := b.probeSharedHost(ctx, ptyhost.HostRegistry{
		DaemonInstanceID: b.cfg.DaemonInstanceID,
		SocketPath:       monitor.socketPath,
		ControlToken:     monitor.controlToken,
	})
	return true, err
}

func (b *WorkerBackend) handleSharedLifecycleEvent(socketPath string, evt ptyworker.EventEnvelope) {
	if evt.SessionID == "" {
		return
	}
	b.mu.RLock()
	session := b.sessions[evt.SessionID]
	b.mu.RUnlock()
	if session == nil || filepath.Clean(session.SocketPath) != filepath.Clean(socketPath) {
		return
	}
	b.handleLifecycleEvent(session, evt)
}

func (b *WorkerBackend) syncSharedSessionLifecycle(session *workerSession) {
	ctx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
	info, err := b.callInfo(ctx, session)
	cancel()
	if err != nil {
		return
	}
	b.mu.RLock()
	current := b.sessions[session.SessionID]
	b.mu.RUnlock()
	if current != session {
		return
	}
	if state := info.State; state != "" {
		detail := info.LastSignalDetail
		source := info.LastSignalSource
		observedAt := info.LastSignalAt
		evt := ptyworker.EventEnvelope{
			Event:           ptyworker.EventStateChanged,
			SessionID:       session.SessionID,
			State:           &state,
			StateDetail:     &detail,
			StateSource:     &source,
			StateObservedAt: &observedAt,
		}
		b.handleLifecycleEvent(session, evt)
	}
	if !info.Running {
		exitCode := 0
		if info.ExitCode != nil {
			exitCode = *info.ExitCode
		}
		b.handleLifecycleEvent(session, ptyworker.EventEnvelope{
			Event:      ptyworker.EventExit,
			SessionID:  session.SessionID,
			ExitCode:   &exitCode,
			ExitSignal: info.ExitSignal,
		})
	}
}

func (b *WorkerBackend) notifySharedHostLost(socketPath string) {
	sessions := b.sharedHostSessions(socketPath)
	b.closeSharedControl(socketPath)
	for _, session := range sessions {
		b.notifySharedHostSessionLost(session)
	}
}

func (b *WorkerBackend) notifySharedHostSessionLost(session *workerSession) {
	session.mu.Lock()
	notifyExit := !session.exitNotified
	session.exitNotified = true
	if session.evictionStarted {
		session.mu.Unlock()
		return
	}
	session.evictionStarted = true
	session.mu.Unlock()
	if notifyExit {
		b.hooksMu.RLock()
		onExit := b.onExit
		b.hooksMu.RUnlock()
		if onExit != nil {
			go onExit(ExitInfo{ID: session.SessionID, ExitCode: 1, Signal: "worker_unreachable", LifecycleID: session.LifecycleID})
		}
	}
	go b.forceSessionEviction(session)
}

func (b *WorkerBackend) spawnShared(ctx context.Context, opts SpawnOptions) error {
	if err := validateUnattendedSpawnOptions(opts); err != nil {
		return err
	}
	if err := validateSessionID(opts.ID); err != nil {
		return err
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}

	prepared, err := pty.PrepareLaunch(sharedPTYSpawnOptions(opts), b.cfg.Logf)
	if err != nil {
		return err
	}
	ownedLaunch := false
	defer func() {
		if !ownedLaunch {
			prepared.CleanupExcept(-1)
		}
	}()

	host, err := b.ensureSharedHost(ctx)
	if err != nil {
		return err
	}
	session := &workerSession{
		SessionID:    opts.ID,
		SocketPath:   host.SocketPath,
		RegistryPath: ptyhost.SessionRegistryPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, opts.ID),
		ControlToken: host.ControlToken,
		WorkerPID:    host.HostPID,
		LifecycleID:  opts.LifecycleID,
	}
	b.mu.Lock()
	if _, exists := b.sessions[opts.ID]; exists {
		b.mu.Unlock()
		return fmt.Errorf("session %s already exists", opts.ID)
	}
	b.sessions[opts.ID] = session
	b.mu.Unlock()
	ready := false
	defer func() {
		if ready {
			return
		}
		b.mu.Lock()
		delete(b.sessions, opts.ID)
		b.mu.Unlock()
	}()

	params := ptyhost.SpawnParams{
		SessionID:   opts.ID,
		Agent:       prepared.Agent,
		CWD:         opts.CWD,
		Label:       opts.Label,
		LifecycleID: opts.LifecycleID,
		Cols:        opts.Cols,
		Rows:        opts.Rows,
		Theme: ptyworker.SetThemeParams{
			Foreground:  opts.Theme.Foreground,
			Background:  opts.Theme.Background,
			Cursor:      opts.Theme.Cursor,
			ANSIPalette: opts.Theme.ANSIPalette,
		},
		Attempts:          prepared.Attempts,
		YoloMode:          opts.YoloMode,
		ApprovalRoute:     opts.ApprovalRoute,
		Executable:        opts.Executable,
		ClaudeExecutable:  opts.ClaudeExecutable,
		CodexExecutable:   opts.CodexExecutable,
		CopilotExecutable: opts.CopilotExecutable,
		Model:             opts.Model,
		Effort:            opts.Effort,
		UnattendedLaunch:  opts.UnattendedLaunch,
	}
	var result ptyhost.SpawnResult
	if err := b.callSharedHost(ctx, host, ptyhost.MethodSpawn, params, &result); err != nil {
		// A dropped response may follow a successful fork. Info is authoritative,
		// so never tear down launch overlays until absence is confirmed.
		if _, probeErr := b.callInfo(ctx, session); probeErr != nil {
			return err
		}
		ownedLaunch = true
	} else {
		ownedLaunch = true
		prepared.CleanupExcept(result.AttemptIndex)
		session.WorkerPID = result.HostPID
	}
	if _, err := b.callInfo(ctx, session); err != nil {
		return fmt.Errorf("shared PTY session did not become ready: %w", err)
	}
	ready = true
	b.startPoller(session)
	b.startMonitor(session)
	b.cfg.Logf("shared PTY host spawn ready: session=%s host_pid=%d child_pid=%d", opts.ID, session.WorkerPID, result.ChildPID)
	return nil
}

func sharedPTYSpawnOptions(opts SpawnOptions) pty.SpawnOptions {
	return pty.SpawnOptions{
		ID:                      opts.ID,
		CWD:                     opts.CWD,
		Agent:                   opts.Agent,
		Label:                   opts.Label,
		Cols:                    opts.Cols,
		Rows:                    opts.Rows,
		ResumeSessionID:         opts.ResumeSessionID,
		ResumePicker:            opts.ResumePicker,
		YoloMode:                opts.YoloMode,
		InitialPromptFile:       opts.InitialPromptFile,
		Executable:              opts.Executable,
		ClaudeExecutable:        opts.ClaudeExecutable,
		CodexExecutable:         opts.CodexExecutable,
		CopilotExecutable:       opts.CopilotExecutable,
		ExternalCommand:         opts.ExternalCommand,
		ExternalEnv:             opts.ExternalEnv,
		ExternalCWD:             opts.ExternalCWD,
		DaemonEnv:               opts.DaemonEnv,
		LifecycleID:             opts.LifecycleID,
		LoginShellEnv:           opts.LoginShellEnv,
		WorkflowGuidanceEnabled: opts.WorkflowGuidanceEnabled,
		AutoApprove:             opts.AutoApprove,
		TrustWorkingDirectory:   opts.TrustWorkingDirectory,
		Model:                   opts.Model,
		Effort:                  opts.Effort,
		ContextWindowCap:        opts.ContextWindowCap,
		UnattendedLaunch:        opts.UnattendedLaunch,
		Theme:                   opts.Theme,
	}
}

func (b *WorkerBackend) ensureSharedHost(ctx context.Context) (ptyhost.HostRegistry, error) {
	b.hostMu.Lock()
	defer b.hostMu.Unlock()

	binary := b.resolveBinaryPath()
	generation, err := ptyhost.Generation(binary, buildinfo.SnapshotFormat)
	if err != nil {
		return ptyhost.HostRegistry{}, err
	}
	registryPath := ptyhost.HostRegistryPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, generation)
	expectedSocket, err := ptyhost.SocketPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, generation)
	if err != nil {
		return ptyhost.HostRegistry{}, err
	}
	if entry, readErr := ptyhost.ReadHostRegistry(registryPath); readErr == nil {
		if err := b.validateCurrentSharedHostEntry(entry, generation, expectedSocket); err != nil {
			return ptyhost.HostRegistry{}, err
		}
		if pidAlive(entry.HostPID) {
			if err := b.probeSharedHost(ctx, entry); err != nil {
				return ptyhost.HostRegistry{}, fmt.Errorf("live shared PTY host is unreachable: %w", err)
			}
			return entry, nil
		}
		_ = os.Remove(registryPath)
		_ = os.Remove(expectedSocket)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ptyhost.HostRegistry{}, fmt.Errorf("read shared PTY host registry: %w", readErr)
	} else if _, statErr := os.Stat(expectedSocket); statErr == nil {
		return ptyhost.HostRegistry{}, fmt.Errorf("shared PTY socket exists without a host registry: %s", expectedSocket)
	}

	token, err := randomToken(32)
	if err != nil {
		return ptyhost.HostRegistry{}, err
	}
	args := []string{
		"--daemon-instance-id", b.cfg.DaemonInstanceID,
		"--generation", generation,
		"--socket-path", expectedSocket,
		"--registry-dir", ptyhost.RegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID),
		"--host-registry-path", registryPath,
		"--control-token", token,
	}
	cmd := exec.Command(binary, args...)
	logPath := ptyhost.LogPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return ptyhost.HostRegistry{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ptyhost.HostRegistry{}, fmt.Errorf("open shared PTY host log: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(withoutEnvironmentKeys(os.Environ(), "ATTN_PTY_WORKER", "ATTN_PTY_HOST"), "ATTN_PTY_HOST=1")
	if err := cmd.Start(); err != nil {
		return ptyhost.HostRegistry{}, fmt.Errorf("start shared PTY host: %w", err)
	}
	pid := cmd.Process.Pid
	go func() {
		if waitErr := cmd.Wait(); waitErr != nil {
			b.cfg.Logf("shared PTY host exited: pid=%d err=%v", pid, waitErr)
		}
	}()

	deadline := time.Now().Add(spawnReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			b.stopSharedHostPID(pid)
			return ptyhost.HostRegistry{}, err
		}
		entry, readErr := ptyhost.ReadHostRegistry(registryPath)
		if readErr == nil && entry.HostPID == pid && entry.ControlToken == token {
			if entryErr := b.validateCurrentSharedHostEntry(entry, generation, expectedSocket); entryErr != nil {
				b.stopSharedHostPID(pid)
				return ptyhost.HostRegistry{}, entryErr
			}
			if probeErr := b.probeSharedHost(ctx, entry); probeErr == nil {
				return entry, nil
			} else {
				lastErr = probeErr
			}
		} else if readErr != nil {
			lastErr = readErr
		}
		if !pidAlive(pid) {
			return ptyhost.HostRegistry{}, fmt.Errorf("shared PTY host exited before ready: %w", lastErr)
		}
		timer := time.NewTimer(spawnReadyPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			b.stopSharedHostPID(pid)
			return ptyhost.HostRegistry{}, ctx.Err()
		case <-timer.C:
		}
	}
	b.stopSharedHostPID(pid)
	return ptyhost.HostRegistry{}, fmt.Errorf("shared PTY host did not become ready: %w", lastErr)
}

func (b *WorkerBackend) validateCurrentSharedHostEntry(entry ptyhost.HostRegistry, generation, expectedSocket string) error {
	if entry.DaemonInstanceID != b.cfg.DaemonInstanceID || entry.Generation != generation || filepath.Clean(entry.SocketPath) != filepath.Clean(expectedSocket) {
		return errors.New("shared PTY host registry identity mismatch")
	}
	return validateSharedHostSnapshotFormat(entry.SnapshotFormat, buildinfo.SnapshotFormat)
}

func validateSharedHostSnapshotFormat(hostFormat, daemonFormat string) error {
	// Raw development and test builds deliberately use portable replay.
	if daemonFormat == "unknown" {
		return nil
	}
	if hostFormat != daemonFormat {
		return fmt.Errorf("shared PTY host snapshot format mismatch: host=%q daemon=%q", hostFormat, daemonFormat)
	}
	return nil
}

func (b *WorkerBackend) stopSharedHostPID(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

func (b *WorkerBackend) probeSharedHost(ctx context.Context, entry ptyhost.HostRegistry) error {
	var result ptyhost.HostInfoResult
	return b.callSharedHost(ctx, entry, ptyhost.MethodHostInfo, map[string]any{}, &result)
}

func (b *WorkerBackend) callSharedHost(ctx context.Context, host ptyhost.HostRegistry, method string, params, result any) error {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	session := &workerSession{SocketPath: host.SocketPath, ControlToken: host.ControlToken}
	conn, enc, dec, err := b.connectWithIdentity(rpcCtx, session, b.cfg.DaemonInstanceID, host.ControlToken)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return err
	}
	reqID := b.nextReqID(method)
	if err := writeRequest(enc, reqID, method, params); err != nil {
		return err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return b.rpcError("", res.Error)
		}
		if result != nil {
			if err := json.Unmarshal(res.Result, result); err != nil {
				return fmt.Errorf("decode shared PTY host %s result: %w", method, err)
			}
		}
		return nil
	}
}
