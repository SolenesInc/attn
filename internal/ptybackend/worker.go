package ptybackend

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

const (
	defaultRPCTimeout       = 5 * time.Second
	killRPCTimeout          = ptyworker.TeardownRPCTimeout
	livenessRPCTimeout      = 2 * time.Second
	monitorRetryInterval    = 1 * time.Second
	watchResponseTimeout    = 5 * time.Second
	pollerUnreachableAfter  = 30 * time.Second
	streamPublishWait       = 250 * time.Millisecond
	streamEventBufferSize   = 256
	streamPreEventBufferCap = 8
)

var (
	spawnReadyTimeout = 45 * time.Second
)

type WorkerBackendConfig struct {
	DataRoot         string
	DaemonInstanceID string
	BinaryPath       string
	Logf             func(format string, args ...interface{})
	OnTerminalBuild  func(id harness.TerminalID)

	OnSharedArtifactRejected func(SharedArtifactRejection) error
}

type workerSession struct {
	SessionID    string
	SocketPath   string
	RegistryPath string
	ControlToken string
	WorkerPID    int
	LifecycleID  string

	mu                  sync.Mutex
	controlMu           sync.Mutex
	exitObserved        *ExitInfo
	evictionStarted     bool
	monitorStop         chan struct{}
	monitorDone         chan struct{}
	legacyLifecycle     bool
	snapshotFormat      string
	snapshotFormatKnown bool
}

type WorkerBackend struct {
	cfg WorkerBackendConfig

	mu       sync.RWMutex
	sessions map[string]*workerSession

	hooksMu sync.RWMutex
	onExit  func(ExitInfo)
	onState func(id harness.TerminalID, obs pty.Observation)
	onTitle func(id harness.TerminalID, title string)

	reqSeq atomic.Uint64

	hostMu sync.Mutex

	sharedControlMu sync.Mutex
	sharedControls  map[hostIncarnation]*sharedHostControl

	sharedMonitorMu sync.Mutex
	sharedMonitors  map[hostIncarnation]*sharedHostMonitor
	sharedStopping  bool

	artifactsDir     string
	candidate        sharedCandidate
	validateMu       sync.Mutex
	artifactMu       sync.Mutex
	pinned           ptyhost.Artifact
	candidateVerdict string

	handoverMu sync.Mutex
	handovers  map[hostIncarnation]*sharedHandover
}

func NewSharedHost(cfg WorkerBackendConfig) (*WorkerBackend, error) {
	if strings.TrimSpace(cfg.DataRoot) == "" {
		return nil, fmt.Errorf("missing data root")
	}
	if strings.TrimSpace(cfg.DaemonInstanceID) == "" {
		return nil, fmt.Errorf("missing daemon instance id")
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = firstExecutable(sharedHostBinaryCandidates())
		if cfg.BinaryPath == "" {
			cfg.BinaryPath = "attn-pty-host"
		}
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...interface{}) {}
	}

	b := &WorkerBackend{
		cfg:            cfg,
		sessions:       make(map[string]*workerSession),
		sharedControls: make(map[hostIncarnation]*sharedHostControl),
		sharedMonitors: make(map[hostIncarnation]*sharedHostMonitor),
		handovers:      make(map[hostIncarnation]*sharedHandover),
	}
	b.loadSharedArtifacts()
	if err := os.MkdirAll(b.registryDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker registry dir: %w", err)
	}
	if err := os.MkdirAll(b.sockDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker socket dir: %w", err)
	}
	if err := os.MkdirAll(b.quarantineDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker quarantine dir: %w", err)
	}
	if err := os.MkdirAll(b.logDir(), 0700); err != nil {
		return nil, fmt.Errorf("create worker log dir: %w", err)
	}
	return b, nil
}

func sharedHostBinaryCandidates() []string {
	var candidates []string
	if explicit := strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")); explicit != "" {
		candidates = append(candidates, explicit)
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(dir, ptyhost.BinaryNameForInstance(config.Instance())),
			filepath.Join(dir, ptyhost.BinaryName),
		)
	}
	home, _ := os.UserHomeDir()
	for _, daemonBinary := range bundledDaemonCandidates(home) {
		candidates = append(candidates, filepath.Join(filepath.Dir(daemonBinary), ptyhost.BinaryName))
	}
	return candidates
}

func firstExecutable(candidates []string) string {
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func bundledDaemonCandidates(home string) []string {
	candidates := []string{config.AppDaemonBinaryForInstance(config.Instance())}
	if runtime.GOOS == "darwin" {
		if strings.TrimSpace(home) != "" {
			candidates = append(candidates, filepath.Join(home, "Applications", "attn.app", "Contents", "MacOS", "attnd"))
		}
		candidates = append(candidates, filepath.Join(string(filepath.Separator), "Applications", "attn.app", "Contents", "MacOS", "attnd"))
	}
	unique := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		unique = append(unique, candidate)
	}
	return unique
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}

func (b *WorkerBackend) SetExitHandler(handler func(ExitInfo)) {
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	b.onExit = handler
}

func (b *WorkerBackend) PTYBackendMode() string {
	return "shared"
}

func (b *WorkerBackend) SetStateHandler(handler func(id harness.TerminalID, obs pty.Observation)) {
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	b.onState = handler
}

func (b *WorkerBackend) SetTitleHandler(handler func(id harness.TerminalID, title string)) {
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	b.onTitle = handler
}

func (b *WorkerBackend) reportTitle(session *workerSession, title string) {
	b.hooksMu.RLock()
	handler := b.onTitle
	b.hooksMu.RUnlock()
	if handler != nil {
		handler(harness.TerminalID(session.SessionID), title)
	}
}

func (b *WorkerBackend) reportState(session *workerSession, observation pty.Observation) {
	b.hooksMu.RLock()
	onState := b.onState
	b.hooksMu.RUnlock()
	if onState != nil {
		onState(harness.TerminalID(session.SessionID), observation)
	}
}

func (b *WorkerBackend) reportExit(session *workerSession, exitCode int, signal string) {
	info := ExitInfo{ID: harness.TerminalID(session.SessionID), ExitCode: exitCode, Signal: signal, LifecycleID: session.LifecycleID}
	session.mu.Lock()
	if session.exitObserved != nil {
		session.mu.Unlock()
		return
	}
	session.exitObserved = &info
	session.mu.Unlock()
	b.hooksMu.RLock()
	onExit := b.onExit
	b.hooksMu.RUnlock()
	if onExit != nil {
		go onExit(info)
	}
}

func (b *WorkerBackend) Probe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	err := b.ValidateSharedCandidate(ctx, true)
	if err != nil && b.SharedArtifactReady() {
		b.cfg.Logf("shared PTY host keeps its last-known-good artifact: %v", err)
		return nil
	}
	return err
}

func withoutEnvironmentKeys(env []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[key]; found {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func (b *WorkerBackend) Attach(ctx context.Context, id harness.TerminalID, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	return b.attachSession(ctx, session, subscriberID, opts...)
}

func (b *WorkerBackend) attachSession(ctx context.Context, session *workerSession, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error) {
	sessionID := session.SessionID
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		_ = conn.Close()
		return AttachInfo{}, nil, err
	}

	attachReqID := b.nextReqID("attach")
	omitReplay := len(opts) > 0 && opts[len(opts)-1].OmitReplay
	if err := writeRequest(enc, attachReqID, ptyworker.MethodAttach, ptyworker.AttachParams{
		SubscriberID: subscriberID,
		OmitReplay:   omitReplay,
	}); err != nil {
		_ = conn.Close()
		return AttachInfo{}, nil, err
	}

	preEvents := make([]OutputEvent, 0, streamPreEventBufferCap)
	var attachResult ptyworker.AttachResult
	for {
		frameType, res, evt, err := readFrame(dec)
		if err != nil {
			_ = conn.Close()
			return AttachInfo{}, nil, err
		}
		switch frameType {
		case "evt":
			if converted, ok := convertWorkerEvent(evt); ok {
				preEvents = appendCappedPreEvent(preEvents, converted, streamPreEventBufferCap)
			}
		case "res":
			if res.ID != attachReqID {
				continue
			}
			if !res.OK {
				_ = conn.Close()
				return AttachInfo{}, nil, b.rpcError(sessionID, res.Error)
			}
			if err := json.Unmarshal(res.Result, &attachResult); err != nil {
				_ = conn.Close()
				return AttachInfo{}, nil, fmt.Errorf("decode attach result: %w", err)
			}
			_ = conn.SetDeadline(time.Time{})
			stream := newWorkerStream(conn, enc, dec, sessionID, b.nextReqID("detach"), preEvents, b.cfg.Logf)
			return AttachInfo{
				LastSeq:                    attachResult.LastSeq,
				Cols:                       attachResult.Cols,
				Rows:                       attachResult.Rows,
				PID:                        attachResult.PID,
				Running:                    attachResult.Running,
				ExitCode:                   attachResult.ExitCode,
				ExitSignal:                 attachResult.ExitSignal,
				GhosttySnapshot:            attachResult.GhosttySnapshot,
				GhosttySnapshotFormat:      attachResult.GhosttySnapshotFormat,
				GhosttyBlocks:              attachBlocksFromWire(attachResult.GhosttyBlocks),
				GhosttyPlacements:          ptyworker.PlacementsFromWire(attachResult.GhosttyPlacements),
				GhosttyScrollbackTruncated: attachResult.GhosttyScrollbackTruncated,
			}, stream, nil
		}
	}
}

func appendCappedPreEvent(events []OutputEvent, evt OutputEvent, capLimit int) []OutputEvent {
	if capLimit <= 0 {
		return events
	}
	if len(events) < capLimit {
		return append(events, evt)
	}
	copy(events, events[1:])
	events[len(events)-1] = evt
	return events
}

func attachBlocksFromWire(blocks []ptyworker.AttachBlock) []pty.AttachBlockData {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]pty.AttachBlockData, len(blocks))
	for i, b := range blocks {
		out[i] = pty.AttachBlockData{
			ID:             b.ID,
			Pending:        b.Pending,
			PromptRow:      b.PromptRow,
			InputRow:       b.InputRow,
			InputCol:       b.InputCol,
			OutputStartRow: b.OutputStartRow,
			EndRow:         b.EndRow,
			OutputStartCol: b.OutputStartCol,
			EndCol:         b.EndCol,
			EndInclusive:   b.EndInclusive,
			Command:        b.Command,
			ExitCode:       b.ExitCode,
		}
	}
	return out
}

func (b *WorkerBackend) Input(ctx context.Context, id harness.TerminalID, data []byte) error {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	return b.inputSession(ctx, session, data)
}

func (b *WorkerBackend) inputSession(ctx context.Context, session *workerSession, data []byte) error {
	payload := ptyworker.InputParams{Data: base64.StdEncoding.EncodeToString(data)}
	err := b.callSimple(ctx, session, ptyworker.MethodInput, payload)
	if err != nil && b.cfg.Logf != nil {
		b.cfg.Logf("worker backend input failed: session=%s bytes=%d err=%v", session.SessionID, len(data), err)
	}
	return err
}

func (b *WorkerBackend) PlaceInput(ctx context.Context, id harness.TerminalID, data []byte, within time.Duration) error {
	session, err := b.getSession(string(id))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, within+defaultRPCTimeout)
	defer cancel()
	payload := ptyworker.InputParams{Data: base64.StdEncoding.EncodeToString(data), PlaceWithinMs: within.Milliseconds()}
	return b.callResultOneShot(ctx, session, ptyworker.MethodInput, payload, nil)
}

func (b *WorkerBackend) Resize(ctx context.Context, id harness.TerminalID, cols, rows, xpixel, ypixel uint16) (ResizeResult, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return ResizeResult{}, err
	}
	return b.resizeSession(ctx, session, cols, rows, xpixel, ypixel)
}

func (b *WorkerBackend) resizeSession(ctx context.Context, session *workerSession, cols, rows, xpixel, ypixel uint16) (ResizeResult, error) {
	var result ptyworker.ResizeResult
	retried, err := b.callResultPersistent(ctx, session, ptyworker.MethodResize, ptyworker.ResizeParams{
		Cols: cols, Rows: rows, XPixel: xpixel, YPixel: ypixel,
	}, &result)
	if err != nil {
		return ResizeResult{}, err
	}
	streamOrdered := result.StreamOrdered != nil && *result.StreamOrdered
	if retried {
		return ResizeResult{Changed: true, StreamOrdered: streamOrdered}, nil
	}
	return ResizeResult{Changed: resizeResultChanged(result), StreamOrdered: streamOrdered}, nil
}

func resizeResultChanged(result ptyworker.ResizeResult) bool {
	if result.Changed == nil {
		return true
	}
	return *result.Changed
}

func (b *WorkerBackend) SetTheme(ctx context.Context, id harness.TerminalID, theme pty.TerminalTheme) error {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	err = b.callSimple(ctx, session, ptyworker.MethodSetTheme, ptyworker.SetThemeParams{
		Foreground:  theme.Foreground,
		Background:  theme.Background,
		Cursor:      theme.Cursor,
		ANSIPalette: theme.ANSIPalette,
	})
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "unknown method") {
		b.cfg.Logf("worker backend set_theme unsupported by worker (old worker, ignoring): session=%s", sessionID)
		return nil
	}
	return err
}

func (b *WorkerBackend) Kill(ctx context.Context, id harness.TerminalID, sig syscall.Signal) error {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, killRPCTimeout)
		defer cancel()
	}
	session.controlMu.Lock()
	defer session.controlMu.Unlock()
	return b.callEndingLocked(ctx, session, ptyworker.MethodSignal, ptyworker.SignalParams{Signal: signalName(sig)})
}

const removedRuntimeExitCode = 128 + int(syscall.SIGTERM)

func (b *WorkerBackend) Remove(ctx context.Context, id harness.TerminalID) error {
	session, err := b.getSession(string(id))
	if err != nil {
		return err
	}
	return b.removeSession(ctx, session)
}

func (b *WorkerBackend) RemoveRuntime(ctx context.Context, id harness.TerminalID, lifecycleID string) error {
	b.mu.RLock()
	session := b.sessions[string(id)]
	b.mu.RUnlock()
	if session == nil || session.LifecycleID != lifecycleID {
		return fmt.Errorf("%w: %s", pty.ErrSessionNotFound, id)
	}
	return b.removeSession(ctx, session)
}

func (b *WorkerBackend) removeSession(ctx context.Context, session *workerSession) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, killRPCTimeout)
		defer cancel()
	}
	callErr := func() error {
		session.controlMu.Lock()
		defer session.controlMu.Unlock()
		err := b.callEndingLocked(ctx, session, ptyworker.MethodRemove, map[string]any{})
		if err == nil || errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			b.forgetSession(session)
			b.reportExit(session, removedRuntimeExitCode, signalName(syscall.SIGTERM))
		}
		return err
	}()
	if callErr != nil && !errors.Is(callErr, pty.ErrSessionNotFound) && !errors.Is(callErr, os.ErrNotExist) {
		return callErr
	}
	b.stopMonitor(session)
	if callErr != nil {
		b.pruneSessionFiles(session.RegistryPath)
	}
	return callErr
}

func (b *WorkerBackend) callEndingLocked(ctx context.Context, session *workerSession, method string, params any) error {
	b.mu.RLock()
	current := b.sessions[session.SessionID] == session
	b.mu.RUnlock()
	if !current {
		return fmt.Errorf("%w: %s", pty.ErrSessionNotFound, session.SessionID)
	}
	return b.callResultOneShot(ctx, session, method, params, nil)
}

func (b *WorkerBackend) forgetSession(session *workerSession) {
	b.mu.Lock()
	if b.sessions[session.SessionID] == session {
		delete(b.sessions, session.SessionID)
	}
	b.mu.Unlock()
	b.releaseSharedIncarnationIfUnused(incarnationOf(session))
}

func (b *WorkerBackend) TerminalIDs(_ context.Context) []harness.TerminalID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]harness.TerminalID, 0, len(b.sessions))
	for id := range b.sessions {
		ids = append(ids, harness.TerminalID(id))
	}
	return ids
}

var _ WorkerProcessProvider = (*WorkerBackend)(nil)

func (b *WorkerBackend) RuntimeLifecycle(id harness.TerminalID) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	session := b.sessions[string(id)]
	if session == nil {
		return "", false
	}
	return session.LifecycleID, true
}

func (b *WorkerBackend) WorkerPIDs(_ context.Context) map[string]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	pids := make(map[string]int, len(b.sessions))
	for id, session := range b.sessions {
		if session.WorkerPID > 0 {
			pids[id] = session.WorkerPID
		}
	}
	return pids
}

func (b *WorkerBackend) Recover(ctx context.Context) (RecoveryReport, error) {
	report := RecoveryReport{}

	files, err := filepath.Glob(filepath.Join(b.registryDir(), "*.json"))
	if err != nil {
		return report, err
	}

	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		entry, err := ptyworker.ReadRegistry(path)
		if err != nil {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		if entry.Version != 1 || entry.SessionID == "" || entry.SocketPath == "" {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		if err := validateSessionID(entry.SessionID); err != nil {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}
		expectedSocketPath, err := b.validateRegistrySocketPath(entry.SessionID, entry.SocketPath)
		if err != nil {
			report.Failed++
			b.quarantineRegistry(path, "socket_path_mismatch")
			continue
		}
		if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
			report.Failed++
			b.quarantineRegistry(path, "ownership_mismatch")
			b.cfg.Logf("PTY recovery ownership mismatch for session %s (pid=%d): preserving the process", entry.SessionID, entry.WorkerPID)
			continue
		}
		if !pidAlive(entry.WorkerPID) {
			report.Pruned++
			_ = os.Remove(path)
			continue
		}

		session := &workerSession{
			SessionID:    entry.SessionID,
			SocketPath:   expectedSocketPath,
			RegistryPath: path,
			ControlToken: entry.ControlToken,
			WorkerPID:    entry.WorkerPID,
		}
		if err := b.probeRecoveryInfo(ctx, session); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
				report.Pruned++
				_ = os.Remove(path)
				continue
			}
			if isTransientRecoveryError(err) {
				report.Missing++
				b.cfg.Logf("worker recovery transient failure: session=%s err=%v", session.SessionID, err)
				continue
			}
			report.Failed++
			b.quarantineRegistry(path, "rpc_unavailable")
			continue
		}
		target := session
		b.mu.Lock()
		if existing := b.sessions[session.SessionID]; existing != nil {
			target = existing
		} else {
			b.sessions[session.SessionID] = session
		}
		b.mu.Unlock()
		b.startSharedHostMonitor(target)
		report.Recovered++
	}
	return report, nil
}

func (b *WorkerBackend) SessionInfo(ctx context.Context, id harness.TerminalID) (SessionInfo, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	info, err := b.callInfo(ctx, session)
	if err != nil {
		return SessionInfo{}, err
	}
	result := SessionInfo{
		SessionID:  sessionID,
		Agent:      info.Agent,
		CWD:        info.CWD,
		Running:    info.Running,
		State:      info.State,
		Title:      info.Title,
		Cols:       info.Cols,
		Rows:       info.Rows,
		PID:        info.ChildPID,
		LastSeq:    info.LastSeq,
		ExitCode:   info.ExitCode,
		ExitSignal: info.ExitSignal,
	}
	if claim := strings.TrimSpace(info.LastSignalClaim); claim != "" {
		at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(info.LastSignalAt))
		if err == nil {
			result.LastSignal = pty.Observation{
				Source: pty.Source(info.LastSignalSource),
				Claim:  claim,
				Detail: info.LastSignalDetail,
				At:     at,
			}
			result.HasLastSignal = true
		}
	}
	return result, nil
}

func (b *WorkerBackend) SessionTerminalBuild(id harness.TerminalID) (format string, known bool) {
	sessionID := string(id)
	b.mu.RLock()
	session := b.sessions[sessionID]
	b.mu.RUnlock()
	if session == nil {
		return "", false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.snapshotFormat, session.snapshotFormatKnown
}

func (b *WorkerBackend) SessionLaunchParams(ctx context.Context, id harness.TerminalID) (SessionLaunchParams, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return SessionLaunchParams{}, err
	}
	entry, err := ptyworker.ReadRegistry(session.RegistryPath)
	if err != nil {
		return SessionLaunchParams{}, err
	}
	return SessionLaunchParams{
		Recorded:         entry.LaunchParamsRecorded,
		YoloMode:         entry.YoloMode,
		ApprovalRoute:    entry.ApprovalRoute,
		Executable:       entry.Executable,
		ClaudeExecutable: entry.ClaudeExecutable,
		CodexExecutable:  entry.CodexExecutable,
		Model:            entry.Model,
		Effort:           entry.Effort,
		UnattendedLaunch: entry.UnattendedLaunch,
	}, nil
}

func (b *WorkerBackend) ScreenSnapshot(ctx context.Context, id harness.TerminalID) (pty.ScreenSnapshotInfo, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return pty.ScreenSnapshotInfo{}, err
	}
	res, err := b.callScreenSnapshot(ctx, session)
	if err != nil {
		return pty.ScreenSnapshotInfo{}, err
	}
	info := pty.ScreenSnapshotInfo{
		LastSeq: res.LastSeq,
		Cols:    res.Cols,
		Rows:    res.Rows,
		Running: res.Running,
	}
	if len(res.ScreenSnapshot) > 0 {
		info.Screen = &pty.ViewportSnapshot{
			Payload: res.ScreenSnapshot,
			Cols:    res.ScreenCols,
			Rows:    res.ScreenRows,
		}
		if res.ScreenText != nil {
			info.Screen.Text = *res.ScreenText
			info.Screen.HasText = true
		}
	}
	return info, nil
}

func (b *WorkerBackend) KittyImage(ctx context.Context, id harness.TerminalID, imageID uint32) (pty.KittyImage, error) {
	sessionID := string(id)
	session, err := b.getSession(sessionID)
	if err != nil {
		return pty.KittyImage{}, err
	}
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return pty.KittyImage{}, err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return pty.KittyImage{}, err
	}

	reqID := b.nextReqID("kitty-image")
	if err := writeRequest(enc, reqID, ptyworker.MethodKittyImage, ptyworker.KittyImageParams{ImageID: imageID}); err != nil {
		return pty.KittyImage{}, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return pty.KittyImage{}, err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return pty.KittyImage{}, b.rpcError(sessionID, res.Error)
		}
		var result ptyworker.KittyImageResult
		if err := json.Unmarshal(res.Result, &result); err != nil {
			return pty.KittyImage{}, fmt.Errorf("decode kitty image result: %w", err)
		}
		return result.Decode()
	}
}

func (b *WorkerBackend) UpgradeWorker(ctx context.Context, id harness.TerminalID) error {
	return b.upgradeSharedTerminal(ctx, string(id))
}

func (b *WorkerBackend) SessionLikelyAlive(ctx context.Context, id harness.TerminalID) (bool, error) {
	sessionID := string(id)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSessionID(sessionID); err != nil {
		return false, nil
	}
	registryPath := filepath.Join(b.registryDir(), sessionID+".json")
	entry, err := ptyworker.ReadRegistry(registryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read worker registry for %s: %w", sessionID, err)
	}
	if entry.SessionID != sessionID || entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
		return false, nil
	}
	socketPath, err := b.validateRegistrySocketPath(sessionID, entry.SocketPath)
	if err != nil {
		return false, nil
	}
	if !pidAlive(entry.WorkerPID) {
		return false, nil
	}
	if _, err := os.Stat(socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat worker socket for %s: %w", sessionID, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, livenessRPCTimeout)
	defer cancel()
	session := &workerSession{
		SessionID:    sessionID,
		SocketPath:   socketPath,
		RegistryPath: registryPath,
		ControlToken: entry.ControlToken,
	}
	if err := b.callSimpleWithIdentity(
		probeCtx,
		session,
		b.cfg.DaemonInstanceID,
		entry.ControlToken,
		ptyworker.MethodHealth,
		map[string]any{},
	); err != nil {
		if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("worker liveness probe failed for %s: %w", sessionID, err)
	}
	return true, nil
}

func (b *WorkerBackend) Shutdown(_ context.Context) error {
	b.mu.RLock()
	sessions := make([]*workerSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.mu.RUnlock()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(session *workerSession) {
			defer wg.Done()
			b.stopMonitor(session)
		}(s)
	}
	wg.Wait()
	b.closeSharedMonitors()
	b.closeSharedControls()
	return nil
}

func (b *WorkerBackend) workerRoot() string {
	return ptyhost.Root(b.cfg.DataRoot, b.cfg.DaemonInstanceID)
}

func (b *WorkerBackend) registryDir() string {
	return ptyhost.RegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID)
}

func (b *WorkerBackend) sockDir() string {
	return filepath.Join(b.workerRoot(), "sock")
}

func (b *WorkerBackend) quarantineDir() string {
	return filepath.Join(b.workerRoot(), "quarantine")
}

func (b *WorkerBackend) logDir() string {
	return filepath.Join(b.workerRoot(), "log")
}

func (b *WorkerBackend) nextReqID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, b.reqSeq.Add(1))
}

func (b *WorkerBackend) getSession(sessionID string) (*workerSession, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}

	b.mu.RLock()
	session := b.sessions[sessionID]
	b.mu.RUnlock()
	if session != nil {
		return session, nil
	}

	registryPath := filepath.Join(b.registryDir(), sessionID+".json")
	entry, err := ptyworker.ReadRegistry(registryPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			b.cfg.Logf("worker backend getSession: read registry failed: session=%s err=%v", sessionID, err)
		}
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if entry.SessionID != sessionID {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if entry.DaemonInstanceID != b.cfg.DaemonInstanceID {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	socketPath, err := b.validateRegistrySocketPath(sessionID, entry.SocketPath)
	if err != nil {
		b.quarantineRegistry(registryPath, "socket_path_mismatch")
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if !pidAlive(entry.WorkerPID) {
		b.pruneSessionFiles(registryPath)
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if _, err := os.Stat(socketPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			b.cfg.Logf("worker backend getSession: stat socket failed: session=%s path=%s err=%v", sessionID, socketPath, err)
		}
		b.pruneSessionFiles(registryPath)
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	session = &workerSession{
		SessionID:    entry.SessionID,
		SocketPath:   socketPath,
		RegistryPath: registryPath,
		ControlToken: entry.ControlToken,
		WorkerPID:    entry.WorkerPID,
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
	probeErr := b.probeRecoveryInfo(probeCtx, session)
	cancel()
	if probeErr != nil {
		if errors.Is(probeErr, pty.ErrSessionNotFound) || errors.Is(probeErr, os.ErrNotExist) {
			b.pruneSessionFiles(registryPath)
			return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
		}
		return nil, probeErr
	}
	b.mu.Lock()
	existing := b.sessions[sessionID]
	if existing == nil {
		b.sessions[sessionID] = session
	}
	b.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	b.startSharedHostMonitor(session)
	return session, nil
}

func (b *WorkerBackend) callSimple(ctx context.Context, session *workerSession, method string, params any) error {
	return b.callSimplePersistent(ctx, session, method, params)
}

func (b *WorkerBackend) callSimpleWithIdentity(
	ctx context.Context,
	session *workerSession,
	daemonInstanceID string,
	controlToken string,
	method string,
	params any,
) error {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectWithIdentity(rpcCtx, session, daemonInstanceID, controlToken)
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
		return nil
	}
}

func (b *WorkerBackend) callSimplePersistent(ctx context.Context, session *workerSession, method string, params any) error {
	_, err := b.callResultPersistent(ctx, session, method, params, nil)
	return err
}

func (b *WorkerBackend) callResultPersistent(ctx context.Context, session *workerSession, method string, params, result any) (bool, error) {
	if method == ptyworker.MethodSignal || method == ptyworker.MethodRemove {
		return false, b.callResultOneShot(ctx, session, method, params, result)
	}
	return b.callResultSharedPersistent(ctx, session, method, params, result)
}

func isRetryablePersistentConnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op != "" {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "closed network connection")
}

func (b *WorkerBackend) callInfo(ctx context.Context, session *workerSession) (ptyworker.InfoResult, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return ptyworker.InfoResult{}, err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return ptyworker.InfoResult{}, err
	}

	reqID := b.nextReqID("info")
	if err := writeRequest(enc, reqID, ptyworker.MethodInfo, map[string]any{}); err != nil {
		return ptyworker.InfoResult{}, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return ptyworker.InfoResult{}, err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return ptyworker.InfoResult{}, b.rpcError(session.SessionID, res.Error)
		}
		var info ptyworker.InfoResult
		if err := json.Unmarshal(res.Result, &info); err != nil {
			return ptyworker.InfoResult{}, err
		}
		return info, nil
	}
}

func (b *WorkerBackend) callScreenSnapshot(ctx context.Context, session *workerSession) (ptyworker.ScreenSnapshotResult, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, session)
	if err != nil {
		return ptyworker.ScreenSnapshotResult{}, err
	}
	defer conn.Close()
	if err := applyConnDeadline(conn, rpcCtx); err != nil {
		return ptyworker.ScreenSnapshotResult{}, err
	}

	reqID := b.nextReqID("snapshot")
	if err := writeRequest(enc, reqID, ptyworker.MethodScreenSnapshot, map[string]any{}); err != nil {
		return ptyworker.ScreenSnapshotResult{}, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return ptyworker.ScreenSnapshotResult{}, err
		}
		if frameType != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return ptyworker.ScreenSnapshotResult{}, b.rpcError(session.SessionID, res.Error)
		}
		var result ptyworker.ScreenSnapshotResult
		if err := json.Unmarshal(res.Result, &result); err != nil {
			return ptyworker.ScreenSnapshotResult{}, err
		}
		return result, nil
	}
}

func (b *WorkerBackend) connectAuthed(ctx context.Context, session *workerSession) (net.Conn, *json.Encoder, *json.Decoder, error) {
	return b.connectWithIdentity(ctx, session, b.cfg.DaemonInstanceID, session.ControlToken)
}

func (b *WorkerBackend) connectWithIdentity(
	ctx context.Context,
	session *workerSession,
	daemonInstanceID string,
	controlToken string,
) (net.Conn, *json.Encoder, *json.Decoder, error) {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", session.SocketPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := applyConnDeadline(conn, ctx); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	helloID := b.nextReqID("hello")
	helloParams := ptyworker.HelloParams{
		RPCMajor:         ptyworker.RPCMajor,
		RPCMinor:         ptyworker.RPCMinor,
		DaemonInstanceID: daemonInstanceID,
		ControlToken:     controlToken,
		SessionID:        session.SessionID,
		SnapshotFormat:   buildinfo.SnapshotFormat,
	}
	if err := writeRequest(enc, helloID, ptyworker.MethodHello, helloParams); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			_ = conn.Close()
			return nil, nil, nil, err
		}
		if frameType != "res" || res.ID != helloID {
			continue
		}
		if !res.OK {
			_ = conn.Close()
			return nil, nil, nil, b.rpcError(session.SessionID, res.Error)
		}
		var hello ptyworker.HelloResult
		if err := json.Unmarshal(res.Result, &hello); err != nil {
			_ = conn.Close()
			return nil, nil, nil, fmt.Errorf("decode hello result: %w", err)
		}
		if !compatibleRPCVersion(hello.RPCMajor) {
			_ = conn.Close()
			return nil, nil, nil, fmt.Errorf(
				"worker rpc version incompatible: got=%d.%d supported=%d.%d..%d.%d",
				hello.RPCMajor, hello.RPCMinor,
				ptyworker.RPCMajor, ptyworker.MinCompatibleRPCMinor,
				ptyworker.RPCMajor, ptyworker.RPCMinor,
			)
		}
		if hello.DaemonInstanceID != daemonInstanceID || hello.SessionID != session.SessionID {
			_ = conn.Close()
			return nil, nil, nil, errors.New("worker identity mismatch")
		}
		session.mu.Lock()
		changed := !session.snapshotFormatKnown || session.snapshotFormat != hello.SnapshotFormat
		session.snapshotFormatKnown = true
		session.snapshotFormat = hello.SnapshotFormat
		session.mu.Unlock()
		if changed && session.SessionID != "" && b.cfg.OnTerminalBuild != nil {
			b.cfg.OnTerminalBuild(harness.TerminalID(session.SessionID))
		}
		break
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, enc, dec, nil
}

func compatibleRPCVersion(major int) bool {
	return major == ptyworker.RPCMajor
}

func (b *WorkerBackend) rpcError(sessionID string, rpcErr *ptyworker.RPCError) error {
	if rpcErr == nil {
		return errors.New("worker rpc error")
	}
	switch rpcErr.Code {
	case ptyworker.ErrSessionNotFound:
		return fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	case ptyworker.ErrImageNotFound:
		return fmt.Errorf("%w: %s", pty.ErrKittyImageNotFound, rpcErr.Message)
	case ptyworker.ErrInputQueueFull:
		return fmt.Errorf("%w: %s", pty.ErrInputQueueFull, rpcErr.Message)
	case ptyworker.ErrSessionNotRunning:
		return errors.New(rpcErr.Message)
	default:
		return errors.New(rpcErr.Message)
	}
}

func (b *WorkerBackend) validateRegistrySocketPath(sessionID, socketPath string) (string, error) {
	if err := ptyhost.ValidateSocketPath(b.cfg.DataRoot, b.cfg.DaemonInstanceID, socketPath); err != nil {
		return "", fmt.Errorf("unexpected shared PTY host socket path for session %s: %w", sessionID, err)
	}
	return filepath.Clean(socketPath), nil
}

func (b *WorkerBackend) workerPIDForSession(session *workerSession) int {
	if session == nil {
		return 0
	}
	if session.WorkerPID > 0 {
		return session.WorkerPID
	}
	if strings.TrimSpace(session.RegistryPath) == "" {
		return 0
	}
	entry, err := ptyworker.ReadRegistry(session.RegistryPath)
	if err != nil || entry.WorkerPID <= 0 {
		return 0
	}
	session.WorkerPID = entry.WorkerPID
	return entry.WorkerPID
}

func (b *WorkerBackend) pruneSessionFiles(registryPath string) {
	_ = os.Remove(registryPath)
}

func (b *WorkerBackend) forceSessionEviction(session *workerSession) {
	workerPID := b.workerPIDForSession(session)
	b.stopMonitor(session)
	b.forgetSession(session)
	b.endProcessGroupOutlivingRuntime(session, workerPID)
	b.pruneSessionFiles(session.RegistryPath)
}

func (b *WorkerBackend) endProcessGroupOutlivingRuntime(session *workerSession, workerPID int) {
	if pidAlive(workerPID) {
		return
	}
	entry, err := ptyworker.ReadRegistry(session.RegistryPath)
	if err != nil || entry.WorkerPID != workerPID || entry.ChildPID <= 0 {
		return
	}
	err = syscall.Kill(-entry.ChildPID, syscall.SIGKILL)
	switch {
	case err == nil:
		b.cfg.Logf("worker backend: killed process group %d of session %s, which outlived its runtime %d", entry.ChildPID, session.SessionID, workerPID)
	case !errors.Is(err, syscall.ESRCH):
		b.cfg.Logf("worker backend: killing process group %d of session %s after its runtime %d ended: %v", entry.ChildPID, session.SessionID, workerPID, err)
	}
}

func (b *WorkerBackend) evictLostSession(session *workerSession) {
	go func() {
		b.forceSessionEviction(session)
		b.reportExit(session, 1, runtimeLostSignal)
	}()
}

var errLifecycleWatchUnsupported = errors.New("worker lifecycle watch unsupported")
var errLifecycleWatchHandshakeTimeout = errors.New("worker lifecycle watch handshake timeout")

func (b *WorkerBackend) startSessionMonitor(session *workerSession) {
	session.mu.Lock()
	if session.monitorStop != nil || session.legacyLifecycle {
		session.mu.Unlock()
		return
	}
	session.monitorStop = make(chan struct{})
	session.monitorDone = make(chan struct{})
	stopCh := session.monitorStop
	doneCh := session.monitorDone
	session.mu.Unlock()

	go func() {
		defer close(doneCh)
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			err := b.runLifecycleMonitor(session, stopCh)
			if err == nil {
				return
			}
			if errors.Is(err, errLifecycleWatchUnsupported) {
				session.mu.Lock()
				session.legacyLifecycle = true
				session.mu.Unlock()
				b.cfg.Logf("worker backend lifecycle watch unsupported for session %s; its exit is noticed on the next call", session.SessionID)
				return
			}
			if errors.Is(err, errLifecycleWatchHandshakeTimeout) {
				session.mu.Lock()
				session.legacyLifecycle = true
				session.mu.Unlock()
				b.cfg.Logf("worker backend lifecycle watch handshake timed out for session %s; its exit is noticed on the next call", session.SessionID)
				return
			}
			b.cfg.Logf("worker backend lifecycle watch disconnected for session %s: %v", session.SessionID, err)
			probeCtx, cancel := context.WithTimeout(context.Background(), livenessRPCTimeout)
			alive, probeErr := b.SessionLikelyAlive(probeCtx, harness.TerminalID(session.SessionID))
			cancel()
			if probeErr == nil && !alive {
				b.notifySharedHostSessionLost(session)
				return
			}
			select {
			case <-stopCh:
				return
			case <-time.After(monitorRetryInterval):
			}
		}
	}()
}

func (b *WorkerBackend) runLifecycleMonitor(session *workerSession, stopCh <-chan struct{}) error {
	callCtx, cancel := withDefaultRPCTimeout(context.Background())
	conn, enc, dec, err := b.connectAuthed(callCtx, session)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()

	stopDone := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
			_ = conn.Close()
		case <-stopDone:
		}
	}()
	defer close(stopDone)

	watchReqID := b.nextReqID("watch")
	if err := writeRequest(enc, watchReqID, ptyworker.MethodWatch, map[string]any{}); err != nil {
		return err
	}

	var handshakeTimedOut atomic.Bool
	handshakeTimer := time.AfterFunc(watchResponseTimeout, func() {
		handshakeTimedOut.Store(true)
		_ = conn.Close()
	})
	defer handshakeTimer.Stop()

	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			select {
			case <-stopCh:
				return nil
			default:
			}
			if handshakeTimedOut.Load() {
				return errLifecycleWatchHandshakeTimeout
			}
			return err
		}
		if frameType != "res" || res.ID != watchReqID {
			continue
		}
		if !res.OK {
			if isLifecycleWatchUnsupported(res.Error) {
				return errLifecycleWatchUnsupported
			}
			return b.rpcError(session.SessionID, res.Error)
		}
		handshakeTimer.Stop()
		break
	}

	for {
		frameType, _, evt, err := readFrame(dec)
		if err != nil {
			select {
			case <-stopCh:
				return nil
			default:
			}
			return err
		}
		if frameType != "evt" {
			continue
		}
		b.handleLifecycleEvent(session, evt)
	}
}

func (b *WorkerBackend) handleLifecycleEvent(session *workerSession, evt ptyworker.EventEnvelope) {
	switch evt.Event {
	case ptyworker.EventTitleChanged:
		if evt.Title != nil {
			b.reportTitle(session, *evt.Title)
		}
	case ptyworker.EventStateChanged:
		if evt.State == nil {
			return
		}
		state := strings.TrimSpace(*evt.State)
		if state == "" {
			return
		}
		now := time.Now()
		observation := ptyworker.ObservationFromEvent(evt, state, now)

		b.reportState(session, observation)
	case ptyworker.EventExit:
		exitCode := 0
		if evt.ExitCode != nil {
			exitCode = *evt.ExitCode
		}
		exitSignal := ""
		if evt.ExitSignal != nil {
			exitSignal = *evt.ExitSignal
		}
		b.reportExit(session, exitCode, exitSignal)
	case ptyworker.EventTeardownEscalated:
		if b.cfg.Logf == nil || evt.Reason == nil || evt.ExitSignal == nil {
			return
		}
		b.cfg.Logf(
			"session teardown escalated for %s: requested=%s escalation=%s",
			session.SessionID,
			strings.TrimSpace(*evt.Reason),
			strings.TrimSpace(*evt.ExitSignal),
		)
	}
}

func isLifecycleWatchUnsupported(rpcErr *ptyworker.RPCError) bool {
	if rpcErr == nil {
		return false
	}
	if rpcErr.Code == ptyworker.ErrUnsupportedVersion {
		return true
	}
	if rpcErr.Code != ptyworker.ErrBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "unknown method")
}

func (b *WorkerBackend) stopMonitor(session *workerSession) {
	session.mu.Lock()
	stopCh := session.monitorStop
	doneCh := session.monitorDone
	session.monitorStop = nil
	session.monitorDone = nil
	session.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
}

func writeRequest(enc *json.Encoder, id, method string, params any) error {
	return writeRequestForSession(enc, id, method, "", params)
}

func writeRequestForSession(enc *json.Encoder, id, method, sessionID string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if string(payload) == "null" {
		payload = nil
	}
	return enc.Encode(ptyworker.RequestEnvelope{
		Type:      "req",
		ID:        id,
		Method:    method,
		SessionID: sessionID,
		Params:    payload,
	})
}

func readMatchingResponse(dec *json.Decoder, reqID string) (ptyworker.ResponseEnvelope, error) {
	for {
		frameType, res, _, err := readFrame(dec)
		if err != nil {
			return ptyworker.ResponseEnvelope{}, err
		}
		if frameType == "res" && res.ID == reqID {
			return res, nil
		}
	}
}

type workerFrame struct {
	ptyworker.EventEnvelope
	ID     string              `json:"id"`
	OK     bool                `json:"ok"`
	Result json.RawMessage     `json:"result,omitempty"`
	Error  *ptyworker.RPCError `json:"error,omitempty"`
}

func readFrame(dec *json.Decoder) (string, ptyworker.ResponseEnvelope, ptyworker.EventEnvelope, error) {
	var frame workerFrame
	if err := dec.Decode(&frame); err != nil {
		return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, err
	}
	switch frame.Type {
	case "res":
		return "res", ptyworker.ResponseEnvelope{
			Type:   frame.Type,
			ID:     frame.ID,
			OK:     frame.OK,
			Result: frame.Result,
			Error:  frame.Error,
		}, ptyworker.EventEnvelope{}, nil
	case "evt":
		return "evt", ptyworker.ResponseEnvelope{}, frame.EventEnvelope, nil
	default:
		return "", ptyworker.ResponseEnvelope{}, ptyworker.EventEnvelope{}, errors.New("unknown frame type")
	}
}

func convertWorkerEvent(evt ptyworker.EventEnvelope) (OutputEvent, bool) {
	switch evt.Event {
	case ptyworker.EventOutput:
		if evt.Data == nil {
			return OutputEvent{}, false
		}
		seq := uint32(0)
		if evt.Seq != nil {
			seq = *evt.Seq
		}
		return OutputEvent{Kind: OutputEventKindOutput, Data: evt.Data, Seq: seq}, true
	case ptyworker.EventDesync:
		reason := ""
		if evt.Reason != nil {
			reason = *evt.Reason
		}
		return OutputEvent{Kind: OutputEventKindDesync, Reason: reason}, true
	case ptyworker.EventResize:
		if evt.Cols == nil || evt.Rows == nil {
			return OutputEvent{}, false
		}
		var xpixel, ypixel uint16
		if evt.XPixel != nil {
			xpixel = *evt.XPixel
		}
		if evt.YPixel != nil {
			ypixel = *evt.YPixel
		}
		return OutputEvent{
			Kind: OutputEventKindResize,
			Cols: *evt.Cols, Rows: *evt.Rows,
			XPixel: xpixel, YPixel: ypixel,
		}, true
	case ptyworker.EventKittyPlacements:
		seq := uint32(0)
		if evt.Seq != nil {
			seq = *evt.Seq
		}
		return OutputEvent{
			Kind:       OutputEventKindPlacements,
			Seq:        seq,
			Placements: ptyworker.PlacementsFromWire(evt.Placements),
		}, true
	default:
		return OutputEvent{}, false
	}
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return "SIGTERM"
	}
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func randomToken(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (b *WorkerBackend) quarantineRegistry(path, reason string) {
	base := filepath.Base(path)
	dest := filepath.Join(
		b.quarantineDir(),
		fmt.Sprintf("%s.%s.%d", base, reason, time.Now().Unix()),
	)
	if err := os.Rename(path, dest); err != nil {
		b.cfg.Logf("worker registry quarantine failed: path=%s reason=%s err=%v", path, reason, err)
		return
	}
	b.cfg.Logf("worker registry quarantined: path=%s reason=%s dest=%s", path, reason, dest)
}

type workerStream struct {
	conn        net.Conn
	enc         *json.Encoder
	dec         *json.Decoder
	sessionID   string
	detachReqID string
	logf        func(format string, args ...interface{})

	events    chan OutputEvent
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	closed    chan struct{}
}

func (s *workerStream) log(format string, args ...interface{}) {
	if s != nil && s.logf != nil {
		s.logf(format, args...)
	}
}

func newWorkerStream(conn net.Conn, enc *json.Encoder, dec *json.Decoder, sessionID, detachReqID string, pre []OutputEvent, logf func(format string, args ...interface{})) *workerStream {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	s := &workerStream{
		conn:        conn,
		enc:         enc,
		dec:         dec,
		sessionID:   sessionID,
		detachReqID: detachReqID,
		logf:        logf,
		events:      make(chan OutputEvent, streamEventBufferSize),
		done:        make(chan struct{}),
		closed:      make(chan struct{}),
	}
	go s.readLoop(pre)
	return s
}

func (s *workerStream) Events() <-chan OutputEvent {
	return s.events
}

func (s *workerStream) Close() error {
	s.closeOnce.Do(func() {
		s.log("worker stream close requested: session=%s", s.sessionID)
		s.doneOnce.Do(func() {
			close(s.done)
		})
		_ = s.conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
		_ = writeRequest(s.enc, s.detachReqID, ptyworker.MethodDetach, map[string]any{})
		_ = s.conn.SetWriteDeadline(time.Time{})
		_ = s.conn.Close()
		<-s.closed
	})
	return nil
}

func (s *workerStream) readLoop(pre []OutputEvent) {
	defer func() {
		s.log("worker stream closed: session=%s", s.sessionID)
		_ = s.conn.Close()
		close(s.events)
		close(s.closed)
	}()

	for _, evt := range pre {
		if !s.publish(evt) {
			return
		}
	}

	for {
		frameType, _, evt, err := readFrame(s.dec)
		if err != nil {
			s.log("worker stream read loop exit: session=%s err=%v", s.sessionID, err)
			return
		}
		if frameType != "evt" {
			continue
		}
		converted, ok := convertWorkerEvent(evt)
		if !ok {
			continue
		}
		if converted.Kind == OutputEventKindOutput {
			pausepoint.At(pausepoint.PtyStreamRead)
		}
		if !s.publish(converted) {
			return
		}
	}
}

func (s *workerStream) publish(evt OutputEvent) bool {
	timer := time.NewTimer(streamPublishWait)
	defer timer.Stop()
	loggedWait := false
	for {
		select {
		case <-s.done:
			s.log("worker stream publish aborted after close: session=%s kind=%s seq=%d", s.sessionID, evt.Kind, evt.Seq)
			return false
		case s.events <- evt:
			if loggedWait {
				s.log("worker stream publish resumed: session=%s kind=%s seq=%d", s.sessionID, evt.Kind, evt.Seq)
			}
			return true
		case <-timer.C:
			if !loggedWait {
				s.log("worker stream publish blocked: session=%s kind=%s seq=%d reason=backpressure", s.sessionID, evt.Kind, evt.Seq)
				loggedWait = true
			}
			timer.Reset(streamPublishWait)
		}
	}
}

func withDefaultRPCTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultRPCTimeout)
}

func applyConnDeadline(conn net.Conn, ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return conn.SetDeadline(time.Time{})
}

func (b *WorkerBackend) probeRecoveryInfo(ctx context.Context, session *workerSession) error {
	backoff := 100 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := b.callInfo(attemptCtx, session)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientRecoveryError(err) {
			return err
		}
		if attempt < 2 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			backoff *= 2
		}
	}
	return lastErr
}

func isTransientRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	lowerErr := strings.ToLower(err.Error())
	transientTokens := []string{
		"timeout",
		"temporarily unavailable",
		"connection refused",
		"connection reset",
		"broken pipe",
		"resource temporarily unavailable",
		"i/o timeout",
	}
	for _, token := range transientTokens {
		if strings.Contains(lowerErr, token) {
			return true
		}
	}
	return false
}

func validateSessionID(sessionID string) error {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return errors.New("missing session id")
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id: %q", sessionID)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return fmt.Errorf("invalid session id: %q", sessionID)
		}
	}
	return nil
}

func (b *WorkerBackend) terminalTextRPC(ctx context.Context, session *workerSession, method string, params, result any) (bool, error) {
	if method != ptyworker.MethodWaitBlock {
		var cancel context.CancelFunc
		ctx, cancel = withDefaultRPCTimeout(ctx)
		defer cancel()
	}
	handshakeCtx, cancelHandshake := withDefaultRPCTimeout(ctx)
	conn, enc, dec, err := b.connectAuthed(handshakeCtx, session)
	cancelHandshake()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := applyConnDeadline(conn, ctx); err != nil {
		return false, err
	}
	reqID := b.nextReqID(method)
	if err := writeRequest(enc, reqID, method, params); err != nil {
		return false, err
	}
	for {
		kind, res, _, err := readFrame(dec)
		if err != nil {
			return false, err
		}
		if kind != "res" || res.ID != reqID {
			continue
		}
		if !res.OK {
			return true, b.rpcError(session.SessionID, res.Error)
		}
		return true, json.Unmarshal(res.Result, result)
	}
}
func (b *WorkerBackend) ReadText(ctx context.Context, id harness.TerminalID, mode string, lines int) (pty.ReadTextInfo, error) {
	session, err := b.getSession(string(id))
	if err != nil {
		return pty.ReadTextInfo{}, err
	}
	var result pty.ReadTextInfo
	_, err = b.terminalTextRPC(ctx, session, ptyworker.MethodReadText, ptyworker.ReadTextParams{Mode: mode, Lines: lines}, &result)
	return result, err
}
func (b *WorkerBackend) WaitBlock(ctx context.Context, id harness.TerminalID, params pty.WaitBlockParams) (pty.WaitBlockResult, error) {
	session, err := b.getSession(string(id))
	if err != nil {
		return pty.WaitBlockResult{}, err
	}
	var result pty.WaitBlockResult
	pausepoint.At(pausepoint.PtyWaitBlock)
	answered, err := b.terminalTextRPC(ctx, session, ptyworker.MethodWaitBlock, params, &result)
	if !answered && err != nil && ctx.Err() == nil {
		session.mu.Lock()
		exit := session.exitObserved
		session.mu.Unlock()
		if exit != nil && exit.Signal != runtimeLostSignal {
			return pty.WaitBlockResult{Exited: true, ExitCode: &exit.ExitCode}, nil
		}
	}
	return result, err
}
