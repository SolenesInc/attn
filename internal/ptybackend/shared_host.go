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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

var sharedHostIdleTimeout time.Duration

type hostIncarnation struct {
	socketPath   string
	controlToken string
}

func incarnationOf(session *workerSession) hostIncarnation {
	return hostIncarnation{socketPath: filepath.Clean(session.SocketPath), controlToken: session.ControlToken}
}

func incarnationOfHost(host ptyhost.HostRegistry) hostIncarnation {
	return hostIncarnation{socketPath: filepath.Clean(host.SocketPath), controlToken: host.ControlToken}
}

func (inc hostIncarnation) endpoint() *workerSession {
	return &workerSession{SocketPath: inc.socketPath, ControlToken: inc.controlToken}
}

type sharedHostControl struct {
	mu          sync.Mutex
	incarnation hostIncarnation
	retired     bool
	conn        net.Conn
	enc         *json.Encoder
	dec         *json.Decoder
}

type sharedHostMonitor struct {
	incarnation hostIncarnation
	stop        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	fallback    bool
	stopping    bool
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
	res, err := readMatchingResponse(dec, reqID)
	if err != nil {
		return err
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

func (b *WorkerBackend) callResultSharedPersistent(ctx context.Context, session *workerSession, method string, params, result any) (bool, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	control := b.lockSharedControl(incarnationOf(session))
	defer control.mu.Unlock()

	if err := b.ensureSharedControlLocked(rpcCtx, control); err != nil {
		return false, err
	}
	delivered, err := b.callResultOnSharedControlLocked(rpcCtx, control, session.SessionID, method, params, result)
	if err == nil || delivered || !isRetryablePersistentConnError(err) || rpcCtx.Err() != nil {
		return false, err
	}
	if err := b.ensureSharedControlLocked(rpcCtx, control); err != nil {
		return true, err
	}
	_, err = b.callResultOnSharedControlLocked(rpcCtx, control, session.SessionID, method, params, result)
	return true, err
}

func (b *WorkerBackend) lockSharedControl(inc hostIncarnation) *sharedHostControl {
	for {
		b.sharedControlMu.Lock()
		control := b.sharedControls[inc]
		if control == nil {
			control = &sharedHostControl{incarnation: inc}
			b.sharedControls[inc] = control
		}
		b.sharedControlMu.Unlock()
		control.mu.Lock()
		if !control.retired {
			return control
		}
		control.mu.Unlock()
	}
}

func (b *WorkerBackend) ensureSharedControlLocked(ctx context.Context, control *sharedHostControl) error {
	if control.conn != nil && control.enc != nil && control.dec != nil {
		return nil
	}
	conn, enc, dec, err := b.connectAuthed(ctx, control.incarnation.endpoint())
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

func (b *WorkerBackend) callResultOnSharedControlLocked(ctx context.Context, control *sharedHostControl, sessionID, method string, params, result any) (delivered bool, err error) {
	if control.conn == nil || control.enc == nil || control.dec == nil {
		return false, errors.New("shared PTY host control connection is not initialized")
	}
	conn := control.conn
	if err := applyConnDeadline(conn, ctx); err != nil {
		b.closeSharedControlLocked(control)
		return false, err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	reqID := b.nextReqID(method)
	if err := writeRequestForSession(control.enc, reqID, method, sessionID, params); err != nil {
		b.closeSharedControlLocked(control)
		return false, err
	}
	res, err := readMatchingResponse(control.dec, reqID)
	if err != nil {
		b.closeSharedControlLocked(control)
		return true, err
	}
	if !res.OK {
		return true, b.rpcError(sessionID, res.Error)
	}
	if result != nil {
		if err := json.Unmarshal(res.Result, result); err != nil {
			return true, fmt.Errorf("decode shared PTY host %s result: %w", method, err)
		}
	}
	return true, nil
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
	b.sharedControls = make(map[hostIncarnation]*sharedHostControl)
	b.sharedControlMu.Unlock()
	for _, control := range controls {
		control.mu.Lock()
		control.retired = true
		b.closeSharedControlLocked(control)
		control.mu.Unlock()
	}
}

func (b *WorkerBackend) closeSharedControl(inc hostIncarnation) {
	b.sharedControlMu.Lock()
	control := b.sharedControls[inc]
	delete(b.sharedControls, inc)
	b.sharedControlMu.Unlock()
	if control == nil {
		return
	}
	control.mu.Lock()
	control.retired = true
	b.closeSharedControlLocked(control)
	control.mu.Unlock()
}

func (b *WorkerBackend) releaseSharedIncarnationIfUnused(inc hostIncarnation) {
	if len(b.sharedHostSessions(inc)) > 0 {
		return
	}
	b.closeSharedControl(inc)
	b.sharedMonitorMu.Lock()
	monitor := b.sharedMonitors[inc]
	released := monitor != nil && b.dropSharedHostMonitorIfUnusedLocked(monitor)
	b.sharedMonitorMu.Unlock()
	if released {
		monitor.stopOnce.Do(func() { close(monitor.stop) })
	}
}

func (b *WorkerBackend) startSharedHostMonitor(session *workerSession) {
	inc := incarnationOf(session)
	b.sharedMonitorMu.Lock()
	if b.sharedStopping {
		b.sharedMonitorMu.Unlock()
		return
	}
	monitor := b.sharedMonitors[inc]
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
		incarnation: inc,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	b.sharedMonitors[inc] = monitor
	b.sharedMonitorMu.Unlock()
	go b.serveSharedHostMonitor(monitor)
}

func (b *WorkerBackend) serveSharedHostMonitor(monitor *sharedHostMonitor) {
	defer func() {
		b.sharedMonitorMu.Lock()
		if b.sharedMonitors[monitor.incarnation] == monitor && !monitor.fallback {
			delete(b.sharedMonitors, monitor.incarnation)
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
			b.cfg.Logf("shared PTY host lifecycle stream unavailable at %s; falling back to per-session streams", monitor.incarnation.socketPath)
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
		sessions := b.sharedHostSessions(monitor.incarnation)
		b.cfg.Logf("shared PTY host lifecycle stream disconnected at %s: %v", monitor.incarnation.socketPath, err)
		alive, probeErr := b.sharedHostLikelyAlive(monitor, sessions)
		if !alive {
			b.notifySharedHostLost(monitor.incarnation)
			return
		}
		if probeErr == nil {
			unreachableAt = time.Time{}
		} else if unreachableAt.IsZero() {
			unreachableAt = time.Now()
		} else if time.Since(unreachableAt) >= pollerUnreachableAfter {
			b.cfg.Logf("shared PTY host remained unreachable at %s: %v", monitor.incarnation.socketPath, probeErr)
			b.notifySharedHostLost(monitor.incarnation)
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
	conn, enc, dec, err := b.connectAuthed(callCtx, monitor.incarnation.endpoint())
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
			b.handleSharedLifecycleEvent(monitor.incarnation, evt)
		}
	}
}

func (b *WorkerBackend) installSharedHostMonitorFallback(monitor *sharedHostMonitor) {
	b.sharedMonitorMu.Lock()
	if b.sharedStopping || monitor.stopping || b.sharedMonitors[monitor.incarnation] != monitor {
		b.sharedMonitorMu.Unlock()
		return
	}
	monitor.fallback = true
	b.sharedMonitorMu.Unlock()
	for _, session := range b.sharedHostSessions(monitor.incarnation) {
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
	b.sharedMonitors = make(map[hostIncarnation]*sharedHostMonitor)
	b.sharedMonitorMu.Unlock()
	for _, monitor := range monitors {
		monitor.stopOnce.Do(func() { close(monitor.stop) })
		<-monitor.done
	}
}

func (b *WorkerBackend) dropSharedHostMonitorIfUnused(monitor *sharedHostMonitor) bool {
	b.sharedMonitorMu.Lock()
	defer b.sharedMonitorMu.Unlock()
	return b.dropSharedHostMonitorIfUnusedLocked(monitor)
}

func (b *WorkerBackend) dropSharedHostMonitorIfUnusedLocked(monitor *sharedHostMonitor) bool {
	if b.sharedStopping || monitor.stopping || b.sharedMonitors[monitor.incarnation] != monitor {
		return true
	}
	if len(b.sharedHostSessions(monitor.incarnation)) > 0 {
		return false
	}
	monitor.stopping = true
	delete(b.sharedMonitors, monitor.incarnation)
	return true
}

func (b *WorkerBackend) sharedHostSessions(inc hostIncarnation) []*workerSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sessions := make([]*workerSession, 0)
	for _, session := range b.sessions {
		if incarnationOf(session) == inc {
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
	if _, err := os.Stat(monitor.incarnation.socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
	defer cancel()
	return true, b.probeSharedHost(ctx, monitor.incarnation)
}

func (b *WorkerBackend) handleSharedLifecycleEvent(inc hostIncarnation, evt ptyworker.EventEnvelope) {
	if evt.SessionID == "" {
		return
	}
	b.mu.RLock()
	session := b.sessions[evt.SessionID]
	b.mu.RUnlock()
	if session == nil || incarnationOf(session) != inc {
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

func (b *WorkerBackend) notifySharedHostLost(inc hostIncarnation) {
	sessions := b.sharedHostSessions(inc)
	b.closeSharedControl(inc)
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
		b.reportExit(session, 1, "worker_unreachable")
	}
	go b.forceSessionEviction(session)
}

func (b *WorkerBackend) spawnShared(ctx context.Context, opts SpawnOptions) error {
	if err := validateSpawnOptions(opts); err != nil {
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

	prepared, err := pty.PrepareLaunch(toPTYSpawnOptions(opts), b.cfg.Logf)
	if err != nil {
		return err
	}
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
	result, spawned, err := b.spawnOnSharedHost(ctx, nil, params)
	switch {
	case !spawned:
		prepared.CleanupExcept(-1)
	case result != nil:
		prepared.CleanupExcept(result.AttemptIndex)
	}
	return err
}

func (b *WorkerBackend) spawnOnSharedHost(ctx context.Context, artifact *ptyhost.Artifact, params ptyhost.SpawnParams) (result *ptyhost.SpawnResult, spawned bool, err error) {
	for attempt := 0; ; attempt++ {
		host, err := b.ensureSharedHost(ctx, artifact)
		if err != nil {
			return nil, false, err
		}
		session := &workerSession{
			SessionID:    params.SessionID,
			SocketPath:   host.SocketPath,
			RegistryPath: ptyhost.SessionRegistryPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, params.SessionID),
			ControlToken: host.ControlToken,
			WorkerPID:    host.HostPID,
			LifecycleID:  params.LifecycleID,
			probe:        params.Agent == probeAgent,
		}
		b.mu.Lock()
		if _, exists := b.sessions[params.SessionID]; exists {
			b.mu.Unlock()
			return nil, false, fmt.Errorf("session %s already exists", params.SessionID)
		}
		b.sessions[params.SessionID] = session
		b.mu.Unlock()

		result, spawned, err := b.spawnSharedSession(ctx, host, session, params)
		if err == nil {
			return result, true, nil
		}
		if spawned {
			b.removeUnreadySharedSession(ctx, session)
		}
		b.mu.Lock()
		delete(b.sessions, params.SessionID)
		b.mu.Unlock()
		b.releaseSharedIncarnationIfUnused(incarnationOf(session))
		if spawned || attempt > 0 || !isRetiringSharedHost(err) {
			return result, spawned, err
		}
		b.cfg.Logf("shared PTY host %s retired before spawning %s; starting another", host.SocketPath, params.SessionID)
	}
}

func (b *WorkerBackend) removeUnreadySharedSession(ctx context.Context, session *workerSession) {
	removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRPCTimeout)
	defer cancel()
	if err := b.callResultSharedOneShot(removeCtx, session, ptyworker.MethodRemove, map[string]any{}, nil); err != nil {
		b.cfg.Logf("remove shared PTY session %s that never became ready: %v", session.SessionID, err)
	}
}

func (b *WorkerBackend) spawnSharedSession(ctx context.Context, host ptyhost.HostRegistry, session *workerSession, params ptyhost.SpawnParams) (*ptyhost.SpawnResult, bool, error) {
	var result ptyhost.SpawnResult
	if err := b.callSharedHost(ctx, incarnationOfHost(host), ptyhost.MethodSpawn, params, &result); err != nil {
		if _, probeErr := b.callInfo(ctx, session); probeErr != nil {
			return nil, false, err
		}
		return nil, true, b.startSharedSession(ctx, session, 0)
	}
	session.WorkerPID = result.HostPID
	return &result, true, b.startSharedSession(ctx, session, result.ChildPID)
}

func (b *WorkerBackend) startSharedSession(ctx context.Context, session *workerSession, childPID int) error {
	if _, err := b.callInfo(ctx, session); err != nil {
		return fmt.Errorf("shared PTY session did not become ready: %w", err)
	}
	b.startMonitor(session)
	b.cfg.Logf("shared PTY host spawn ready: session=%s host_pid=%d child_pid=%d", session.SessionID, session.WorkerPID, childPID)
	return nil
}

func isRetiringSharedHost(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) ||
		strings.Contains(err.Error(), "host is shutting down")
}

func (b *WorkerBackend) ensureSharedHost(ctx context.Context, artifact *ptyhost.Artifact) (ptyhost.HostRegistry, error) {
	b.hostMu.Lock()
	defer b.hostMu.Unlock()
	if artifact == nil {
		launch, err := b.launchArtifact()
		if err != nil {
			return ptyhost.HostRegistry{}, err
		}
		artifact = &launch
	}
	if host, ok := b.liveSharedHost(ctx, artifact.ID); ok {
		return host, nil
	}
	return b.startSharedHost(ctx, *artifact)
}

func (b *WorkerBackend) liveSharedHost(ctx context.Context, artifactID string) (ptyhost.HostRegistry, bool) {
	paths, _ := filepath.Glob(filepath.Join(ptyhost.HostRegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID), "*.json"))
	for _, path := range paths {
		entry, err := ptyhost.ReadHostRegistry(path)
		if err != nil || entry.ArtifactID != artifactID || b.validateSharedHostEntry(entry) != nil {
			continue
		}
		if !pidAlive(entry.HostPID) {
			_ = os.Remove(path)
			_ = os.Remove(entry.SocketPath)
			continue
		}
		if b.probeSharedHost(ctx, incarnationOfHost(entry)) == nil {
			return entry, true
		}
	}
	return ptyhost.HostRegistry{}, false
}

func (b *WorkerBackend) startSharedHost(ctx context.Context, artifact ptyhost.Artifact) (ptyhost.HostRegistry, error) {
	incarnation, socketPath, err := b.newSharedHostIncarnation()
	if err != nil {
		return ptyhost.HostRegistry{}, err
	}
	registryPath := ptyhost.HostRegistryPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, incarnation)
	token, err := randomToken(32)
	if err != nil {
		return ptyhost.HostRegistry{}, err
	}
	args := []string{
		"--daemon-instance-id", b.cfg.DaemonInstanceID,
		"--generation", artifact.ID,
		"--socket-path", socketPath,
		"--registry-dir", ptyhost.RegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID),
		"--host-registry-path", registryPath,
		"--control-token", token,
	}
	if sharedHostIdleTimeout > 0 {
		args = append(args, "--idle-timeout-ms", strconv.FormatInt(sharedHostIdleTimeout.Milliseconds(), 10))
	}
	cmd := exec.Command(artifact.Path, args...)
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
	b.cfg.Logf("shared PTY host starting: artifact=%s incarnation=%s pid=%d", artifact.ID, incarnation, pid)

	deadline := time.Now().Add(spawnReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			b.stopSharedHostPID(pid)
			return ptyhost.HostRegistry{}, err
		}
		entry, readErr := ptyhost.ReadHostRegistry(registryPath)
		if readErr == nil && entry.HostPID == pid && entry.ControlToken == token {
			if entry.ArtifactID != artifact.ID || filepath.Clean(entry.SocketPath) != filepath.Clean(socketPath) ||
				b.validateSharedHostEntry(entry) != nil {
				b.stopSharedHostPID(pid)
				return ptyhost.HostRegistry{}, errors.New("shared PTY host registry identity mismatch")
			}
			if probeErr := b.probeSharedHost(ctx, incarnationOfHost(entry)); probeErr == nil {
				return entry, nil
			} else {
				lastErr = probeErr
			}
		} else if readErr != nil {
			lastErr = readErr
		}
		if !pidAlive(pid) {
			return ptyhost.HostRegistry{}, fmt.Errorf("%w: shared PTY host exited before ready: %w", errArtifactRejected, lastErr)
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

func (b *WorkerBackend) newSharedHostIncarnation() (incarnation, socketPath string, err error) {
	for range 4 {
		incarnation, err = randomToken(8)
		if err != nil {
			return "", "", err
		}
		socketPath, err = ptyhost.SocketPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, incarnation)
		if err != nil {
			return "", "", err
		}
		if _, statErr := os.Lstat(socketPath); errors.Is(statErr, os.ErrNotExist) {
			return incarnation, socketPath, nil
		}
	}
	return "", "", errors.New("no free shared PTY host socket path")
}

func (b *WorkerBackend) validateSharedHostEntry(entry ptyhost.HostRegistry) error {
	if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
		return errors.New("shared PTY host registry identity mismatch")
	}
	return ptyhost.ValidateSocketPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, entry.SocketPath)
}

func (b *WorkerBackend) stopSharedHostPID(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

func (b *WorkerBackend) probeSharedHost(ctx context.Context, inc hostIncarnation) error {
	_, err := b.sharedHostInfo(ctx, inc)
	return err
}

func (b *WorkerBackend) sharedHostInfo(ctx context.Context, inc hostIncarnation) (ptyhost.HostInfoResult, error) {
	var result ptyhost.HostInfoResult
	err := b.callSharedHost(ctx, inc, ptyhost.MethodHostInfo, map[string]any{}, &result)
	return result, err
}

func (b *WorkerBackend) callSharedHost(ctx context.Context, inc hostIncarnation, method string, params, result any) error {
	return b.callResultSharedOneShot(ctx, inc.endpoint(), method, params, result)
}
