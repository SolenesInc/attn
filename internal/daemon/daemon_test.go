package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type countingClassifier struct {
	state string
	mu    sync.Mutex
	calls int
}

func (c *countingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.state, nil
}

func (c *countingClassifier) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type blockingClassifier struct {
	state   string
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func newBlockingClassifier(state string) *blockingClassifier {
	return &blockingClassifier{
		state:   state,
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (c *blockingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-c.release
	return c.state, nil
}

func (c *blockingClassifier) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func waitForRecovery(t *testing.T, d *Daemon) {
	t.Helper()
	<-d.recoverySettledSignal()
}

func TestDaemon_Start_FailsWhenWebSocketPortIsAlreadyBound(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "bindclash")
	addr := net.JoinHostPort(config.WSBindAddress(), useFreeWSPort(t))

	foreign, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("occupy %s: %v", addr, err)
	}
	defer foreign.Close()

	socketPath := filepath.Join(shortTempDir(t), "test.sock")
	d := NewForTesting(socketPath)
	t.Cleanup(d.Stop)

	startErr := make(chan error, 1)
	go func() { startErr <- d.Start() }()

	select {
	case err := <-startErr:
		if err == nil {
			t.Fatal("Start() returned nil while another process held the WebSocket port; a split-brained daemon must be refused")
		}
		for _, want := range []string{addr, "bindclash"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Start() error = %q, want it to name %q", err, want)
			}
		}
		if _, statErr := os.Stat(socketPath); !os.IsNotExist(statErr) {
			t.Errorf("failed start left %s behind (stat: %v); a socket path with no listener is a false ready signal", socketPath, statErr)
		}
	case <-d.startedCh:
		t.Fatal("daemon reported itself started while another process held the WebSocket port; the bind failure must be fatal, not logged")
	}
}

func TestDaemon_Start_FailsBeforeReadyWhenRunnerLockIsHeld(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	useFreeWSPort(t)
	dir := shortTempDir(t)
	holder, err := jobs.AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("hold runner lock: %v", err)
	}

	d := NewForTesting(filepath.Join(dir, "test.sock"))
	err = d.Start()
	if err == nil {
		t.Fatal("Start() succeeded while the runner lock was held")
	}
	for _, want := range []string{"start background jobs", holder.Path(), jobs.ErrAlreadyRunning.Error()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Start() error = %q, want it to name %q", err, want)
		}
	}
	select {
	case <-d.Started():
		t.Fatal("daemon reported itself started while background jobs could not start")
	default:
	}
	select {
	case <-d.done:
	default:
		t.Fatal("failed startup did not stop daemon services")
	}
	if _, statErr := os.Stat(d.socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed startup left socket behind: %v", statErr)
	}
	if _, acceptErr := d.httpListener.Accept(); !errors.Is(acceptErr, net.ErrClosed) {
		t.Fatalf("failed startup left WebSocket listener open: %v", acceptErr)
	}

	pidProbe := &Daemon{pidPath: d.pidPath}
	if err := pidProbe.acquirePIDLock(); err != nil {
		t.Fatalf("failed startup retained daemon ownership: %v", err)
	}
	pidProbe.releasePIDLock()

	holder.Release()
	runnerProbe, err := jobs.AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("runner lock remained unavailable after holder release: %v", err)
	}
	runnerProbe.Release()

	d.Stop()
}

func TestDaemon_Start_SelectsWorkerBackendWhenRequested(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "1")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "worker-select.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	waitForSocket(t, sockPath, 3*time.Second)

	if d.daemonInstanceID == "" {
		t.Fatal("daemon_instance_id should be initialized before backend selection")
	}
	if _, ok := d.ptyBackend.(*ptybackend.WorkerBackend); !ok {
		t.Fatalf("expected worker backend, got %T", d.ptyBackend)
	}
}

func TestDaemon_Start_DefaultUsesMigrationRouterWithoutMovingNewSessionsWhenHostIsUnprobed(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "1")
	t.Setenv("ATTN_PTY_HOST_BINARY", "")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "migrating-select.sock")
	d := NewForTesting(sockPath)
	errCh := make(chan error, 1)
	go func() { errCh <- d.Start() }()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			t.Fatalf("daemon start error: %v", err)
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	if _, ok := d.ptyBackend.(*ptybackend.MigratingBackend); !ok {
		t.Fatalf("expected migration router, got %T", d.ptyBackend)
	}
	if got := d.ptyBackendMode(); got != "migrating" {
		t.Fatalf("PTY backend mode = %q, want migrating", got)
	}
}

func TestDaemon_Start_WorkerProbeFailureFallsBackToEmbedded(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "0")
	t.Setenv("ATTN_PTY_WORKER_BINARY", filepath.Join(t.TempDir(), "missing-attn-binary"))
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "worker-probe-fallback.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	if _, ok := d.ptyBackend.(*ptybackend.EmbeddedBackend); !ok {
		t.Fatalf("expected embedded backend after probe failure, got %T", d.ptyBackend)
	}
	hasFallbackWarning := false
	for _, w := range d.getWarnings() {
		if w.Code == warnPTYBackendFallback {
			hasFallbackWarning = true
			break
		}
	}
	if !hasFallbackWarning {
		t.Fatalf("expected %q warning after worker probe failure", warnPTYBackendFallback)
	}
}

func TestDaemon_Start_SelectsEmbeddedBackendWhenRequested(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "embedded-select.sock")
	d := NewForTesting(sockPath)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()
	if !d.waitStarted(3 * time.Second) {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("daemon start error: %v", err)
			}
			t.Fatal("daemon exited unexpectedly during startup")
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()

	waitForSocket(t, sockPath, 3*time.Second)

	if _, ok := d.ptyBackend.(*ptybackend.EmbeddedBackend); !ok {
		t.Fatalf("expected embedded backend, got %T", d.ptyBackend)
	}
}

func TestDaemon_Start_ReplaysUnreadMailboxAfterPTYRecovery(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "mailbox-replay.sock")
	d := NewForTesting(sockPath)
	doorbell := &recordingDoorbell{}
	recoveryEntered := make(chan struct{})
	releaseRecovery := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRecovery) }) }
	defer release()
	backend := doorbell.backend()
	backend.sessionIDs = []string{"mailbox-restart-target"}
	backend.onRecover = func() {
		close(recoveryEntered)
		<-releaseRecovery
	}
	d.ptyBackend = backend
	addCharacterizationSession(t, d, "mailbox-restart-target", protocol.SessionAgentCodex, protocol.SessionStateIdle)
	if _, err := d.store.EnqueueMaintenancePrompt(
		"mailbox-restart-item", "mailbox-restart-target", "survived restart", time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- d.Start() }()
	if !d.waitStarted(3 * time.Second) {
		release()
		select {
		case err := <-errCh:
			t.Fatalf("daemon start error: %v", err)
		default:
			t.Fatal("daemon did not signal startup")
		}
	}
	defer d.Stop()
	select {
	case <-recoveryEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("PTY recovery did not start")
	}
	if got := doorbell.pasted(); len(got) != 0 {
		t.Fatalf("mailbox replay reached the PTY before recovery completed: %q", got)
	}

	release()
	waitForRecovery(t, d)
	if got := doorbell.pasted(); len(got) != 1 || got[0] != agentMailboxDoorbellText {
		t.Fatalf("mailbox replay after recovery = %q, want one generic doorbell", got)
	}
}

type fakeDeferredRecoveryBackend struct {
	fakeWorkerReconcileBackend
	mu          sync.Mutex
	reports     []ptybackend.RecoveryReport
	likelyAlive map[string]bool
	likelyErr   map[string]error
}

func (b *fakeDeferredRecoveryBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.reports) == 0 {
		return ptybackend.RecoveryReport{}, nil
	}
	report := b.reports[0]
	b.reports = b.reports[1:]
	return report, nil
}

func (b *fakeDeferredRecoveryBackend) SessionLikelyAlive(_ context.Context, sessionID string) (bool, error) {
	if err, ok := b.likelyErr[sessionID]; ok {
		return false, err
	}
	return b.likelyAlive[sessionID], nil
}

type fakeClearSessionsBackend struct {
	mu               sync.Mutex
	sessionIDs       []string
	recoveredIDs     []string
	recoverCalled    bool
	killed           []string
	removed          []string
	killErrBySession map[string]error
}

func (b *fakeClearSessionsBackend) Spawn(context.Context, ptybackend.SpawnOptions) error { return nil }
func (b *fakeClearSessionsBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, nil
}
func (b *fakeClearSessionsBackend) Input(context.Context, string, []byte) error { return nil }
func (b *fakeClearSessionsBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}
func (b *fakeClearSessionsBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *fakeClearSessionsBackend) Kill(_ context.Context, sessionID string, _ syscall.Signal) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killed = append(b.killed, sessionID)
	if b.killErrBySession != nil {
		return b.killErrBySession[sessionID]
	}
	return nil
}
func (b *fakeClearSessionsBackend) Remove(_ context.Context, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, sessionID)
	return nil
}
func (b *fakeClearSessionsBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sessionIDs...)
}
func (b *fakeClearSessionsBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recoverCalled = true
	b.sessionIDs = append(append([]string(nil), b.sessionIDs...), b.recoveredIDs...)
	return ptybackend.RecoveryReport{Recovered: len(b.recoveredIDs)}, nil
}
func (b *fakeClearSessionsBackend) Shutdown(context.Context) error { return nil }

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLivePluginReportedState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "plugin-live",
		Label:          "plugin-live",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-live",
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-live", "snipe-plugin", "run-live") {
		t.Fatal("BeginAgentDriverRun(plugin-live) failed")
	}
	if !d.store.ApplyAgentDriverState("plugin-live", "run-live", 1, protocol.StateWaitingInput, time.Time{}) {
		t.Fatal("ApplyAgentDriverState(plugin-live) failed")
	}
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"plugin-live"},
		info: map[string]ptybackend.SessionInfo{
			"plugin-live": {
				SessionID: "plugin-live",
				Agent:     "snipe",
				CWD:       "/tmp/plugin-live",
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.StateUpdated != 0 {
		t.Fatalf("state_updated = %d, want 0 for plugin-owned state", report.StateUpdated)
	}
	session := d.store.Get("plugin-live")
	if session == nil || session.State != protocol.SessionStateWaitingInput {
		t.Fatalf("plugin-live session = %+v, want waiting_input retained from plugin report", session)
	}
}

func TestDaemon_PruneSessionsWithoutPTY_PreservesPluginMetadataForResume(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "plugin-resume",
		Label:          "plugin-resume",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-resume",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-resume", "snipe-plugin", "run-resume") {
		t.Fatal("BeginAgentDriverRun(plugin-resume) failed")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-resume", "run-resume", 1, `{"native_id":"resume-me"}`) {
		t.Fatal("ApplyAgentDriverMetadata(plugin-resume) failed")
	}
	giveLaunchIntent(t, d, "plugin-resume")

	if removed := d.pruneSessionsWithoutPTY(d.storedSessionIDs(), time.Time{}); removed != 0 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 0", removed)
	}
	session := d.store.Get("plugin-resume")
	if session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("plugin-resume session = %+v, want recoverable plugin session", session)
	}
	if got := d.store.GetAgentMetadata("plugin-resume"); got != `{"native_id":"resume-me"}` {
		t.Fatalf("metadata = %q, want persisted resume metadata", got)
	}
}

func TestDaemon_RunDeferredWorkerReconciliationForcesIdleDemotion(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	d.runDeferredWorkerReconciliation(1, 0, d.storedSessionIDs(), time.Time{})

	session := d.store.Get("stale-running")
	if session != nil {
		t.Fatal("stale-running session should be reaped: no worker and nothing to resume")
	}

	warnings := d.getWarnings()
	hasPartial := false
	for _, w := range warnings {
		if w.Code == "worker_recovery_partial" && strings.Contains(w.Message, "Forced stale-session reconciliation") {
			hasPartial = true
			break
		}
	}
	if !hasPartial {
		t.Fatalf("expected forced reconciliation warning, got %+v", warnings)
	}
}

func TestDaemon_RunDeferredWorkerReconciliation_BroadcastsSessionsUpdatedOnChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	broadcasts := 0
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event != nil && event.Event == protocol.EventSessionsUpdated {
			broadcasts++
		}
	}

	d.runDeferredWorkerReconciliation(1, 0, d.storedSessionIDs(), time.Time{})

	if broadcasts == 0 {
		t.Fatal("expected deferred reconciliation to broadcast sessions_updated after state changes")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLikelyAliveSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyAlive: map[string]bool{
			"stale-running": true,
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LikelyAlive != 1 {
		t.Fatalf("likely_alive = %d, want 1", report.LikelyAlive)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnLivenessProbeError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyErr: map[string]error{
			"stale-running": errors.New("probe timeout"),
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LivenessUnknown != 1 {
		t.Fatalf("liveness_unknown = %d, want 1", report.LivenessUnknown)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnIncompleteRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "missing-running",
		Label:          "missing",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/missing",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), false, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.SkippedIdle != 1 {
		t.Fatalf("skipped_idle = %d, want 1", report.SkippedIdle)
	}
	session := d.store.Get("missing-running")
	if session == nil {
		t.Fatal("missing-running session missing after reconcile")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("missing-running state = %s, want working", session.State)
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemotePTYTrafficToInterestedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientAttached := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientAttached] = true
	d.wsHub.clients[clientOther] = true

	clientAttached.notePendingRemoteAttach("remote-runtime-1")

	attachPayload, err := json.Marshal(protocol.AttachResultMessage{
		Event:   protocol.EventAttachResult,
		ID:      "remote-runtime-1",
		Success: true,
	})
	if err != nil {
		t.Fatalf("marshal attach_result: %v", err)
	}
	d.broadcastRawWSMessage(attachPayload)

	attachEvent := readOutboundEvent(t, clientAttached)
	if asString(attachEvent["event"]) != protocol.EventAttachResult || asString(attachEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected attach event: %+v", attachEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientAttached.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("client should track remote runtime after attach_result success")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientAttached)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)

	resizePayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyResized,
		ID:    protocol.Ptr("remote-runtime-1"),
		Cols:  protocol.Ptr(100),
		Rows:  protocol.Ptr(30),
	})
	if err != nil {
		t.Fatalf("marshal pty_resized: %v", err)
	}
	d.broadcastRawWSMessage(resizePayload)

	resizeEvent := readOutboundEvent(t, clientAttached)
	if asString(resizeEvent["event"]) != protocol.EventPtyResized || asString(resizeEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pty_resized event: %+v", resizeEvent)
	}
	assertNoOutboundEvent(t, clientOther)
}

func TestDaemon_BroadcastRawWSMessage_RoutesPendingRemotePTYOutputBeforeAttachResult(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientPending := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientPending] = true
	d.wsHub.clients[clientOther] = true

	clientPending.notePendingRemoteAttach("remote-runtime-1")

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientPending)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pending-attach pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if clientPending.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("pending attach should not mark remote runtime attached before attach_result")
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemoteTileContentToSubscribedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientSubscribed := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[clientSubscribed] = true
	d.wsHub.clients[clientOther] = true
	clientSubscribed.notePendingTileContent("remote-workspace", "tile-markdown")

	payload, err := json.Marshal(protocol.WorkspaceTileContentMessage{
		Event:       protocol.EventWorkspaceTileContent,
		WorkspaceID: "remote-workspace",
		TileID:      "tile-markdown",
		TileKind:    string(workspacelayout.TileKindMarkdown),
		Path:        "/srv/repo/README.md",
		Content:     "# Private",
	})
	if err != nil {
		t.Fatalf("marshal workspace_tile_content: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	event := readOutboundEvent(t, clientSubscribed)
	if asString(event["event"]) != protocol.EventWorkspaceTileContent || asString(event["content"]) != "# Private" {
		t.Fatalf("unexpected tile content event: %+v", event)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientSubscribed.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("successful relayed tile response should promote the pending request to a subscription")
	}
}

func TestDaemon_BroadcastRawWSMessage_PrunesRemoteTileSubscriptionsAfterLayoutUpdate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent("remote-workspace", "tile-markdown")

	layoutJSON, err := workspacelayout.EncodeLayout(workspacelayout.DefaultLayout("pane-1"))
	if err != nil {
		t.Fatalf("encode layout: %v", err)
	}
	payload, err := json.Marshal(protocol.WorkspaceLayoutUpdatedMessage{
		Event: protocol.EventWorkspaceLayoutUpdated,
		WorkspaceLayout: protocol.WorkspaceLayout{
			WorkspaceID:  "remote-workspace",
			ActivePaneID: "pane-1",
			LayoutJson:   layoutJSON,
		},
	})
	if err != nil {
		t.Fatalf("marshal workspace_layout_updated: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	if client.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("removed remote tile subscription survived layout update")
	}
}

func TestDaemon_BroadcastRawWSMessage_RemoteSessionExitedClearsRemoteAttachState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[client] = true
	client.attachedRemote["remote-runtime-1"] = struct{}{}

	exitPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventSessionExited,
		ID:    protocol.Ptr("remote-runtime-1"),
	})
	if err != nil {
		t.Fatalf("marshal session_exited: %v", err)
	}
	d.broadcastRawWSMessage(exitPayload)

	exitEvent := readOutboundEvent(t, client)
	if asString(exitEvent["event"]) != protocol.EventSessionExited || asString(exitEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected session_exited event: %+v", exitEvent)
	}
	if client.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("session_exited should clear remote attach state")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("late"))),
		Seq:   protocol.Ptr(8),
	})
	if err != nil {
		t.Fatalf("marshal late pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)
	assertNoOutboundEvent(t, client)
}

func TestDaemon_NewAddsWarningWhenPersistenceFallsBackToMemory(t *testing.T) {
	t.Setenv("ATTN_DB_PATH", filepath.Join("/dev/null", "attn.db"))

	d := New(filepath.Join(t.TempDir(), "test.sock"))
	defer d.store.Close()

	warnings := d.getWarnings()
	if len(warnings) == 0 {
		t.Fatal("expected warning when DB open fails and daemon falls back to in-memory")
	}

	found := false
	for _, warning := range warnings {
		if warning.Code != "persistence_degraded" {
			continue
		}
		found = true
		if !strings.Contains(warning.Message, "Running in-memory only") {
			t.Fatalf("warning message missing in-memory note: %q", warning.Message)
		}
		if !strings.Contains(warning.Message, "See daemon log in "+config.LogPath()) {
			t.Fatalf("warning message missing daemon log path: %q", warning.Message)
		}
		if !strings.Contains(warning.Message, "/dev/null/attn.db") {
			t.Fatalf("warning message missing DB path: %q", warning.Message)
		}
	}
	if !found {
		t.Fatalf("expected persistence_degraded warning, got: %+v", warnings)
	}

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "fallback-store-session",
		Label:          "fallback-store-session",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if got := d.store.Get("fallback-store-session"); got == nil {
		t.Fatal("expected in-memory fallback store to remain usable")
	}
}

func TestDaemon_HandleClientMessage_ClearWarnings(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("one", "first warning")
	d.addWarning("two", "second warning")
	if got := len(d.getWarnings()); got != 2 {
		t.Fatalf("warnings before clear = %d, want 2", got)
	}

	client := &wsClient{}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	d.handleClientMessage(client, []byte(`{"cmd":"clear_warnings"}`))

	if got := len(d.getWarnings()); got != 0 {
		t.Fatalf("warnings after clear = %d, want 0", got)
	}
}

func TestDaemon_AddWarning_DedupesByCodeAndMessage(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("worker_recovery_partial", "first")
	d.addWarning("worker_recovery_partial", "second")
	d.addWarning("worker_recovery_partial", "second")

	warnings := d.getWarnings()
	if len(warnings) != 2 {
		t.Fatalf("warnings len = %d, want 2", len(warnings))
	}
}

func TestDaemon_ClearWarningsNotReplayedInInitialState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.addWarning("stale_sessions_pruned", "Removed 1 stale sessions from a previous daemon run because no live PTY was found.")

	client := &wsClient{
		send: make(chan outboundMessage, 4),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	first := <-client.send
	var firstEvent protocol.WebSocketEvent
	if err := json.Unmarshal(first.payload, &firstEvent); err != nil {
		t.Fatalf("decode first initial_state: %v", err)
	}
	if firstEvent.Event != protocol.EventInitialState {
		t.Fatalf("first event = %q, want %q", firstEvent.Event, protocol.EventInitialState)
	}
	if got := len(firstEvent.Warnings); got != 1 {
		t.Fatalf("first initial_state warnings = %d, want 1", got)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"clear_warnings"}`))

	d.sendInitialState(client)
	second := <-client.send
	var secondEvent protocol.WebSocketEvent
	if err := json.Unmarshal(second.payload, &secondEvent); err != nil {
		t.Fatalf("decode second initial_state: %v", err)
	}
	if secondEvent.Event != protocol.EventInitialState {
		t.Fatalf("second event = %q, want %q", secondEvent.Event, protocol.EventInitialState)
	}
	if got := len(secondEvent.Warnings); got != 0 {
		t.Fatalf("second initial_state warnings = %d, want 0", got)
	}
}

func TestDaemon_InitialState_IncludesDaemonInstanceID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.daemonInstanceID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	msg := <-client.send

	var initial protocol.InitialStateMessage
	if err := json.Unmarshal(msg.payload, &initial); err != nil {
		t.Fatalf("decode initial_state: %v", err)
	}
	if protocol.Deref(initial.DaemonInstanceID) != d.daemonInstanceID {
		t.Fatalf("daemon_instance_id = %q, want %q", protocol.Deref(initial.DaemonInstanceID), d.daemonInstanceID)
	}
}

func TestDaemon_GitHubHostsMessages_UseRegisteredHosts(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ghRegistry.Register("ghe.example.test", nil)
	d.ghRegistry.Register("github.com", nil)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.sendInitialState(client)
	msg := <-client.send

	var initial protocol.InitialStateMessage
	if err := json.Unmarshal(msg.payload, &initial); err != nil {
		t.Fatalf("decode initial_state: %v", err)
	}
	if got := strings.Join(initial.GithubHosts, ","); got != "ghe.example.test,github.com" {
		t.Fatalf("initial github_hosts = %q, want registered hosts", got)
	}

	updated := d.gitHubHostsUpdatedMessage()
	if updated.Event != protocol.EventGitHubHostsUpdated {
		t.Fatalf("updated event = %q, want %q", updated.Event, protocol.EventGitHubHostsUpdated)
	}
	if got := strings.Join(updated.GithubHosts, ","); got != "ghe.example.test,github.com" {
		t.Fatalf("updated github_hosts = %q, want registered hosts", got)
	}
}

func TestDaemon_RecoveryBarrier_BlocksPTYCommands(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"attach_session","id":"sess-1"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdAttachSession {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdAttachSession)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
}

func TestDaemon_RecoveryBarrier_BlocksClearSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "sess-1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"clear_sessions"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdClearSessions {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdClearSessions)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
	if got := len(d.store.List("")); got != 1 {
		t.Fatalf("store sessions = %d, want 1 (clear should be blocked during recovery)", got)
	}
}

func TestDaemon_ClearAllSessions_RecoversAndTerminatesKnownSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "store-session",
		Label:          "store-session",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/store-session",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	backend := &fakeClearSessionsBackend{
		sessionIDs:   []string{"attached-session"},
		recoveredIDs: []string{"registry-only-session"},
	}
	d.ptyBackend = backend

	d.clearAllSessions()

	if got := len(d.store.List("")); got != 0 {
		t.Fatalf("store sessions = %d, want 0", got)
	}

	backend.mu.Lock()
	recoverCalled := backend.recoverCalled
	killed := append([]string(nil), backend.killed...)
	removed := append([]string(nil), backend.removed...)
	backend.mu.Unlock()

	if !recoverCalled {
		t.Fatal("expected clearAllSessions to call backend Recover()")
	}
	expectKilled := map[string]bool{
		"store-session":         false,
		"attached-session":      false,
		"registry-only-session": false,
	}
	for _, id := range killed {
		if _, ok := expectKilled[id]; ok {
			expectKilled[id] = true
		}
	}
	for id, seen := range expectKilled {
		if !seen {
			t.Fatalf("expected kill for %s, got kills=%v", id, killed)
		}
	}
	expectRemoved := map[string]bool{
		"store-session":         false,
		"attached-session":      false,
		"registry-only-session": false,
	}
	for _, id := range removed {
		if _, ok := expectRemoved[id]; ok {
			expectRemoved[id] = true
		}
	}
	for id, seen := range expectRemoved {
		if !seen {
			t.Fatalf("expected remove for %s, got removes=%v", id, removed)
		}
	}
}

func TestDaemon_RecoveryBarrier_DefersInitialState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.daemonInstanceID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.scheduleInitialState(client)
	select {
	case <-client.send:
		t.Fatal("initial_state was sent while daemon was recovering")
	default:
	}

	d.setRecovering(false)
	select {
	case msg := <-client.send:
		var initial protocol.InitialStateMessage
		if err := json.Unmarshal(msg.payload, &initial); err != nil {
			t.Fatalf("decode deferred initial_state: %v", err)
		}
		if initial.Event != protocol.EventInitialState {
			t.Fatalf("event = %q, want %q", initial.Event, protocol.EventInitialState)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for deferred initial_state")
	}
}

func TestDaemon_HealthDoesNotReportReadyBeforeStartupCompletes(t *testing.T) {
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	readStatus := func() string {
		recorder := httptest.NewRecorder()
		d.handleHealth(recorder, request)
		var health struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(recorder.Result().Body).Decode(&health); err != nil {
			t.Fatalf("decode health: %v", err)
		}
		return health.Status
	}

	if status := readStatus(); status != "starting" {
		t.Fatalf("health status before startup = %q, want starting", status)
	}
	d.signalStarted()
	if status := readStatus(); status != "ok" {
		t.Fatalf("health status after startup = %q, want ok", status)
	}
}

func TestDaemon_FaviconDoesNot404(t *testing.T) {
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")

	wsPort := useFreeWSPort(t)

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()

	waitForSocket(t, sockPath, 5*time.Second)

	faviconURL := "http://127.0.0.1:" + wsPort + "/favicon.ico"
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(faviconURL)
		if err == nil {
			resp = r
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp == nil {
		t.Fatalf("favicon endpoint not ready after 5s")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read favicon body: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if len(body) != 0 {
		t.Fatalf("favicon body length = %d, want 0", len(body))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store, max-age=0" {
		t.Fatalf("favicon Cache-Control = %q, want no-store, max-age=0", got)
	}
}

func readOutboundEvent(t *testing.T, client *wsClient) map[string]interface{} {
	t.Helper()
	select {
	case outbound := <-client.send:
		var event map[string]interface{}
		if err := json.Unmarshal(outbound.payload, &event); err != nil {
			t.Fatalf("decode outbound event: %v", err)
		}
		return event
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for outbound event")
		return nil
	}
}

func assertNoOutboundEvent(t *testing.T, client *wsClient) {
	t.Helper()
	select {
	case outbound := <-client.send:
		t.Fatalf("unexpected outbound event: %s", string(outbound.payload))
	default:
	}
}

func TestDaemon_SettingsValidation(t *testing.T) {
	d := &Daemon{}

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{"valid projects_directory", "projects_directory", t.TempDir(), false},
		{"valid new_session_agent codex", "new_session_agent", "codex", false},
		{"valid new_session_agent claude", "new_session_agent", "claude", false},
		{"valid new_session_agent copilot", "new_session_agent", "copilot", false},
		{"unregistered future plugin agent pi", "new_session_agent", "pi", true},
		{"empty new_session_agent", "new_session_agent", "", false},
		{"empty claude_executable", "claude_executable", "", false},
		{"empty codex_executable", "codex_executable", "", false},
		{"empty copilot_executable", "copilot_executable", "", false},
		{"empty reviewer_model", "reviewer_model", "", false},
		{"custom reviewer_model", "reviewer_model", "claude-sonnet-4-6", false},
		{"valid gardenScale", "gardenScale", "1.2", false},
		{"empty gardenScale matches app", "gardenScale", "", false},
		{"gardenScale out of range", "gardenScale", "3.0", true},
		{"gardenScale not a number", "gardenScale", "big", true},
		{"retired ticketBoardScale", "ticketBoardScale", "1.2", true},
		{"valid tailscale_enabled true", "tailscale_enabled", "true", false},
		{"valid tailscale_enabled false", "tailscale_enabled", "false", false},
		{"valid queue_crew_enabled true", "queue_crew_enabled", "true", false},
		{"invalid queue_crew_enabled", "queue_crew_enabled", "maybe", true},
		{"empty keybindings_config", "keybindings_config", "", false},
		{"valid keybindings_config", "keybindings_config", `{"version":1,"overrides":{"session.new":{"key":"m","meta":true}}}`, false},
		{"invalid keybindings_config json", "keybindings_config", "{not json", true},
		{"invalid claude_executable", "claude_executable", "not-a-real-binary-123", true},
		{"invalid new_session_agent", "new_session_agent", "gpt", true},
		{"invalid tailscale_enabled", "tailscale_enabled", "maybe", true},
		{"valid sidebar harness logos", "sidebar_harness_logos_enabled", "false", false},
		{"invalid sidebar harness logos", "sidebar_harness_logos_enabled", "sometimes", true},
		{"invalid key", "unknown_setting", "value", true},
		{"empty projects_directory", "projects_directory", "", true},
		{"relative path", "projects_directory", "relative/path", true},
		{"empty chief context cap uses default", "chief_context_window_cap", "", false},
		{"valid chief context cap", "chief_context_window_cap", "128000", false},
		{"chief context cap below min", "chief_context_window_cap", "5000", true},
		{"chief context cap above max", "chief_context_window_cap", "9000000", true},
		{"non-numeric chief context cap", "chief_context_window_cap", "lots", true},
		{"empty per-agent context cap means uncapped", "default_context_window_cap_claude", "", false},
		{"valid per-agent context cap", "default_context_window_cap_claude", "800000", false},
		{"per-agent context cap below min", "default_context_window_cap_codex", "5000", true},
		{"non-numeric per-agent context cap", "default_context_window_cap_claude", "lots", true},
		{"empty headless context cap uses default", "headless_context_window_cap", "", false},
		{"valid headless context cap", "headless_context_window_cap", "200000", false},
		{"headless context cap below min", "headless_context_window_cap", "1", true},
		{"valid chief_effort_claude", "chief_effort_claude", "high", false},
		{"empty chief_effort_claude", "chief_effort_claude", "", false},
		{"valid default_model_claude", "default_model_claude", "opus", false},
		{"empty default_model_claude", "default_model_claude", "", false},
		{"valid default_effort_claude", "default_effort_claude", "high", false},
		{"empty default_effort_claude", "default_effort_claude", "", false},
		{"remembered destination new worktree", "new_session_destination_local_/Users/v/projects/attn", "new_worktree", false},
		{"remembered destination main repo", "new_session_destination_endpoint_ep-1_/srv/projects/attn", "main_repo", false},
		{"forgotten destination", "new_session_destination_local_/Users/v/projects/attn", "", false},
		{"unknown destination", "new_session_destination_local_/Users/v/projects/attn", "somewhere_else", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := d.validateSetting(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSetting(%q, %q) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestDaemon_ContextWindowCapResolutionAndGating(t *testing.T) {
	d := &Daemon{store: store.New()}

	for _, v := range []string{"", "  ", "not-a-number", "0", "-100"} {
		if got := resolveContextWindowCap(v); got != agentdriver.DefaultContextWindowCap {
			t.Fatalf("resolveContextWindowCap(%q) = %d, want default %d", v, got, agentdriver.DefaultContextWindowCap)
		}
	}
	if got := resolveContextWindowCap("200000"); got != 200000 {
		t.Fatalf("resolveContextWindowCap(200000) = %d, want 200000", got)
	}

	if got := d.launchContextWindowCap("sess-1", "claude", false); got != 0 {
		t.Fatalf("non-chief cap with no setting = %d, want 0 (uncapped)", got)
	}
	if got := d.launchContextWindowCap("sess-1", "claude", true); got != agentdriver.DefaultContextWindowCap {
		t.Fatalf("chief cap with no setting = %d, want default %d", got, agentdriver.DefaultContextWindowCap)
	}
	d.store.SetSetting(SettingChiefContextWindowCap, "160000")
	if got := d.launchContextWindowCap("sess-1", "claude", true); got != 160000 {
		t.Fatalf("chief cap = %d, want 160000", got)
	}
	d.store.SetSetting(SettingDefaultContextWindowCapPrefix+"claude", "800000")
	if got := d.launchContextWindowCap("sess-1", "claude", false); got != 800000 {
		t.Fatalf("per-agent default cap = %d, want 800000", got)
	}
	if got := d.launchContextWindowCap("sess-1", "Claude", false); got != 800000 {
		t.Fatalf("per-agent default cap (case-insensitive) = %d, want 800000", got)
	}
	if got := d.launchContextWindowCap("sess-1", "codex", false); got != 0 {
		t.Fatalf("other agent's cap = %d, want 0 (uncapped)", got)
	}
	if got := d.launchContextWindowCap("sess-1", "claude", true); got != 160000 {
		t.Fatalf("chief cap with per-agent default also set = %d, want 160000", got)
	}

	d.store.Add(&protocol.Session{ID: "sess-pinned", Label: "pinned", Agent: protocol.SessionAgentClaude, Directory: "/tmp"})
	if !d.store.SetSessionContextWindowCap("sess-pinned", 300000) {
		t.Fatalf("SetSessionContextWindowCap failed")
	}
	if got := d.launchContextWindowCap("sess-pinned", "claude", false); got != 300000 {
		t.Fatalf("pinned cap = %d, want 300000", got)
	}
	if got := d.launchContextWindowCap("sess-pinned", "claude", true); got != 300000 {
		t.Fatalf("pinned cap on a chief launch = %d, want 300000 (pin outranks chief setting)", got)
	}
	if !d.store.SetSessionContextWindowCap("sess-pinned", 0) {
		t.Fatalf("clear SetSessionContextWindowCap failed")
	}
	if got := d.launchContextWindowCap("sess-pinned", "claude", false); got != 800000 {
		t.Fatalf("cleared pin cap = %d, want per-agent default 800000", got)
	}
}

func TestDaemon_SetSessionContextWindowCap(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.Add(&protocol.Session{ID: "sess-cap", Label: "capped", Agent: protocol.SessionAgentClaude, Directory: "/tmp"})
	d.store.Add(&protocol.Session{ID: "sess-shell", Label: "shell", Agent: protocol.SessionAgentShell, Directory: "/tmp"})

	if err := d.setSessionContextWindowCap("", 200000); err == nil {
		t.Fatalf("missing session_id accepted")
	}
	if err := d.setSessionContextWindowCap("sess-missing", 200000); err == nil {
		t.Fatalf("unknown session accepted")
	}
	if err := d.setSessionContextWindowCap("sess-shell", 200000); err == nil {
		t.Fatalf("shell session accepted a cap it can never apply")
	}
	for _, invalid := range []int{-1, 1, contextWindowCapMin - 1, contextWindowCapMax + 1} {
		if err := d.setSessionContextWindowCap("sess-cap", invalid); err == nil {
			t.Fatalf("out-of-bounds cap %d accepted", invalid)
		}
	}

	if err := d.setSessionContextWindowCap("sess-cap", 500000); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	if got := protocol.Deref(d.store.Get("sess-cap").ContextWindowCap); got != 500000 {
		t.Fatalf("stored cap = %d, want 500000", got)
	}
	if err := d.setSessionContextWindowCap("sess-cap", 500000); err != nil {
		t.Fatalf("idempotent set errored: %v", err)
	}
	if err := d.setSessionContextWindowCap("sess-cap", 0); err != nil {
		t.Fatalf("clear cap: %v", err)
	}
	if d.store.Get("sess-cap").ContextWindowCap != nil {
		t.Fatalf("cleared cap still stored")
	}
}

func TestDaemon_ApplyHeadlessContextWindowCap(t *testing.T) {
	t.Cleanup(func() { agentdriver.SetHeadlessContextWindowCap(0) })

	d := &Daemon{store: store.New()}

	d.applyHeadlessContextWindowCap()
	if got := agentdriver.HeadlessContextWindowCap(); got != agentdriver.DefaultContextWindowCap {
		t.Fatalf("headless cap with no setting = %d, want default %d", got, agentdriver.DefaultContextWindowCap)
	}

	d.store.SetSetting(SettingHeadlessContextWindowCap, "180000")
	d.applyHeadlessContextWindowCap()
	if got := agentdriver.HeadlessContextWindowCap(); got != 180000 {
		t.Fatalf("headless cap = %d, want 180000", got)
	}
}

func TestDaemon_SettingsIncludePTYBackendMode(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))

	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingPTYBackendMode]; got != "embedded" {
		t.Fatalf("settings[%s] = %v, want embedded", SettingPTYBackendMode, got)
	}

	workerBackend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{
		DataRoot:         t.TempDir(),
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}
	d.ptyBackend = workerBackend

	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingPTYBackendMode]; got != "worker" {
		t.Fatalf("settings[%s] = %v, want worker", SettingPTYBackendMode, got)
	}

	sharedBackend, err := ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{
		DataRoot:         t.TempDir(),
		DaemonInstanceID: "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewSharedHost() error: %v", err)
	}
	migrating, err := ptybackend.NewMigrating(workerBackend, sharedBackend, true)
	if err != nil {
		t.Fatalf("NewMigrating() error: %v", err)
	}
	d.ptyBackend = migrating
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingPTYBackendMode]; got != "migrating" {
		t.Fatalf("settings[%s] = %v, want migrating", SettingPTYBackendMode, got)
	}
}

func TestDaemon_SettingsWithClaudeAvailability_InstallsClaudeSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	binDir := t.TempDir()
	claudePath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(claudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude executable: %v", err)
	}
	t.Setenv("PATH", binDir)

	d := &Daemon{store: store.New()}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingClaudeAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingClaudeAvailable, got)
	}

	skillPath := filepath.Join(home, ".claude", "skills", "attn", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected Claude attn skill at %s: %v", skillPath, err)
	}
	delegationPath := filepath.Join(home, ".claude", "skills", "attn", "references", "delegation.md")
	if _, err := os.Stat(delegationPath); err != nil {
		t.Fatalf("expected Claude attn delegation reference at %s: %v", delegationPath, err)
	}
}

func TestDaemon_SettingsWithCodexAvailability_InstallsCodexSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex executable: %v", err)
	}
	t.Setenv("PATH", binDir)

	d := &Daemon{store: store.New()}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingCodexAvailable]; got != "true" {
		t.Fatalf("settings[%s] = %v, want true", SettingCodexAvailable, got)
	}

	skillPath := filepath.Join(home, ".agents", "skills", "attn", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected Codex attn skill at %s: %v", skillPath, err)
	}
	delegationPath := filepath.Join(home, ".agents", "skills", "attn", "references", "delegation.md")
	if _, err := os.Stat(delegationPath); err != nil {
		t.Fatalf("expected Codex attn delegation reference at %s: %v", delegationPath, err)
	}
}

func TestDaemon_StopCommand_PendingTodos_SetsWaitingInput(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	go d.Start()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)

	err := c.Register("test-session", "Test", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	todosMsg := map[string]interface{}{
		"cmd":   "todos",
		"id":    "test-session",
		"todos": []string{"[ ] Pending task 1", "[ ] Pending task 2"},
	}
	todosJSON, _ := json.Marshal(todosMsg)
	conn.Write(todosJSON)

	var resp protocol.Response
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if !resp.Ok {
		t.Fatalf("Todos update failed: %s", protocol.Deref(resp.Error))
	}

	conn2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	stopMsg := map[string]interface{}{
		"cmd":             "stop",
		"id":              "test-session",
		"transcript_path": "/nonexistent/path",
	}
	stopJSON, _ := json.Marshal(stopMsg)
	conn2.Write(stopJSON)
	json.NewDecoder(conn2).Decode(&resp)
	conn2.Close()

	waitForResolvedState(t, d, "test-session", protocol.SessionStateWaitingInput)
}

func TestDaemon_StopCommand_CompletedTodos_ProceedsToClassification(t *testing.T) {
	useFreeWSPort(t)

	sockPath := filepath.Join(shortTempDir(t), "attn.sock")
	os.Remove(sockPath)

	d := NewForTesting(sockPath)
	startErr := make(chan error, 1)
	go func() { startErr <- d.Start() }()
	defer func() {
		d.Stop()
		os.Remove(sockPath)
	}()

	select {
	case <-d.startedCh:
	case err := <-startErr:
		t.Fatalf("Daemon start error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not signal startup")
	}

	c := client.New(sockPath)

	err := c.Register("test-session", "Test", "/tmp/test")
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	todosMsg := map[string]interface{}{
		"cmd":   "todos",
		"id":    "test-session",
		"todos": []string{"[✓] Completed task 1", "[✓] Completed task 2"},
	}
	todosJSON, _ := json.Marshal(todosMsg)
	conn.Write(todosJSON)

	var resp protocol.Response
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if !resp.Ok {
		t.Fatalf("Todos update failed: %s", protocol.Deref(resp.Error))
	}

	sessions, _ := c.Query("")
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if len(sessions[0].Todos) != 2 {
		t.Fatalf("Expected 2 todos, got %d", len(sessions[0].Todos))
	}

	t.Log("Test passed: todos with [✓] prefix are counted as completed, allowing classification to proceed")
}

func TestClassifySessionState_ClassifierErrorAddsNoVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = &errorClassifier{
		state: protocol.StateUnknown,
		err:   errors.New("classifier execution failed"),
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-unknown",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Now running pre-review."}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.recordBracketEvidence("sess-unknown", protocol.StateWorking)
	d.recordPTYEvidence("sess-unknown", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence("sess-unknown", protocol.StateIdle)
	d.recordPTYEvidence("sess-unknown", pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.classifySessionState("sess-unknown", transcriptPath)
	d.resolveDue(time.Now())

	sess := d.store.Get("sess-unknown")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateIdle {
		t.Fatalf("state = %s, want %s: the turn ended, the classifier just could not say how", sess.State, protocol.StateIdle)
	}
}

func TestClassifySessionState_ClassifierCapabilityDisabled_SetsIdle(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_CLASSIFIER", "0")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-no-classifier",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.classifySessionState("sess-no-classifier", filepath.Join(t.TempDir(), "missing.jsonl"))
	d.resolveDue(time.Now())

	sess := d.store.Get("sess-no-classifier")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateIdle {
		t.Fatalf("state = %s, want %s", sess.State, protocol.StateIdle)
	}
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}
}

func TestClassifySessionState_TranscriptDisabledWithPendingTodos_SetsWaitingInput(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_TRANSCRIPT", "0")

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateIdle}
	d.classifier = mockClassifier

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-no-transcript",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
		Todos:          []string{"[ ] follow up"},
	})

	d.classifySessionState("sess-no-transcript", filepath.Join(t.TempDir(), "missing.jsonl"))
	d.resolveDue(time.Now())

	sess := d.store.Get("sess-no-transcript")
	if sess == nil {
		t.Fatal("session missing after classify")
	}
	if sess.State != protocol.StateWaitingInput {
		t.Fatalf("state = %s, want %s", sess.State, protocol.StateWaitingInput)
	}
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}
}

func TestClassifySessionState_SkipsNoNewAssistantTurn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
	d.classifier = mockClassifier
	d.classificationTranscriptExtractor = func(*protocol.Session, string, int, time.Time) (string, string, error) {
		return "", "", agentdriver.ErrNoNewAssistantTurn
	}

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Agent:          protocol.SessionAgentClaude,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	d.classifySessionState("sess-1", filepath.Join(t.TempDir(), "transcript.jsonl"))
	if got := mockClassifier.CallCount(); got != 0 {
		t.Fatalf("classifier calls=%d, want 0", got)
	}

	sess := d.store.Get("sess-1")
	if sess == nil {
		t.Fatal("session missing after classification")
	}
	if sess.State != protocol.StateWorking {
		t.Fatalf("state changed on no-new-turn result: got %q want %q", sess.State, protocol.StateWorking)
	}
}

func TestClassifySessionState_ClaudeConcurrentDuplicateTurnRunsOnce(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		mockClassifier := newBlockingClassifier(protocol.StateWaitingInput)
		d.classifier = mockClassifier

		now := time.Now()
		nowStr := string(protocol.NewTimestamp(now))
		d.store.Add(&protocol.Session{
			ID:             "sess-2",
			Agent:          protocol.SessionAgentClaude,
			Label:          "test",
			Directory:      "/tmp",
			State:          protocol.StateWorking,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		})

		transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
		content := fmt.Sprintf(
			`{"type":"user","uuid":"u2","timestamp":"%s","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a2","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"Hello! What can I help you with today?"}]}}
`,
			now.Add(-1*time.Second).UTC().Format(time.RFC3339Nano),
			now.UTC().Format(time.RFC3339Nano),
		)
		if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}

		firstDone := make(chan struct{})
		go func() {
			d.classifySessionState("sess-2", transcriptPath)
			close(firstDone)
		}()

		synctest.Wait()
		select {
		case <-mockClassifier.started:
		default:
			t.Fatal("classifier did not start for first classification")
		}

		secondDone := make(chan struct{})
		go func() {
			d.classifySessionState("sess-2", transcriptPath)
			close(secondDone)
		}()

		requireDone(t, secondDone, "second classification did not return promptly")

		if got := mockClassifier.CallCount(); got != 1 {
			t.Fatalf("classifier calls=%d, want 1 while duplicate turn in flight", got)
		}

		close(mockClassifier.release)
		requireDone(t, firstDone, "first classification did not complete")

		if got := mockClassifier.CallCount(); got != 1 {
			t.Fatalf("classifier calls=%d, want 1", got)
		}
	})
}

func TestClassifySessionState_PublishesImmediatelyAfterALongRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	mockClassifier := &countingClassifier{state: protocol.StateIdle}
	d.classifier = mockClassifier

	nowStr := string(protocol.NewTimestamp(time.Now()))
	d.store.Add(&protocol.Session{
		ID:             "sess-long",
		Agent:          protocol.SessionAgentCodex,
		Label:          "long",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     string(protocol.NewTimestamp(time.Now().Add(-10 * time.Minute))),
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})

	transcriptPath := filepath.Join(t.TempDir(), "long-transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Completed long run"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.recordBracketEvidence("sess-long", protocol.StateWorking)
	d.recordBracketEvidence("sess-long", protocol.StateIdle)

	d.classifySessionState("sess-long", transcriptPath)
	d.resolveDue(time.Now())

	if got := mockClassifier.CallCount(); got != 1 {
		t.Fatalf("classifier calls=%d, want 1", got)
	}
	session := d.store.Get("sess-long")
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.StateIdle {
		t.Fatalf("state=%s, want %s", session.State, protocol.StateIdle)
	}
}

func TestHandleStop_SkipsClassificationForForcedStopSession(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
		d.classifier = mockClassifier

		now := time.Now()
		nowStr := string(protocol.NewTimestamp(now))
		d.store.Add(&protocol.Session{
			ID:             "sess-forced-stop",
			Agent:          protocol.SessionAgentCodex,
			Label:          "forced-stop",
			Directory:      "/tmp",
			State:          protocol.StateIdle,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		})
		d.markForcedStopClassification("sess-forced-stop")

		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d.handleStop(serverConn, &protocol.StopMessage{
				ID:             "sess-forced-stop",
				TranscriptPath: "",
			})
			_ = serverConn.Close()
		}()

		var resp protocol.Response
		if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
			t.Fatalf("decode stop response: %v", err)
		}
		if !resp.Ok {
			t.Fatalf("stop response ok=%v, want true", resp.Ok)
		}

		requireDone(t, done, "handleStop did not return")

		settleStopClassification(t)
		if got := mockClassifier.CallCount(); got != 0 {
			t.Fatalf("classifier calls=%d, want 0", got)
		}
		if d.consumeForcedStopClassification("sess-forced-stop") {
			t.Fatal("forced-stop suppression token should be consumed by handleStop")
		}
	})
}
