package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

func heartbeat(claim string, at time.Time) pty.Observation {
	return pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: "a turn summary", At: at}
}

func TestRecoveredReviewerKeepsBriefApprovalInsideGuardianDwell(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	concludedAt := time.Now().Add(-time.Minute)
	addRecoveredSession(t, d, "guarded", protocol.SessionStateWorking, concludedAt)
	d.store.SetLaunchIntent("guarded", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteReviewer})
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"guarded"},
		info:    map[string]ptybackend.SessionInfo{"guarded": runningInfo(nil)},
		params: map[string]ptybackend.SessionLaunchParams{
			"guarded": {Recorded: true, ApprovalRoute: launchcontract.ApprovalRouteReviewer},
		},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if !evidenceOf(t, d, "guarded").ReviewerInLoop {
		t.Fatal("recovery did not reconstruct the reviewer before resolver activity")
	}
	now := time.Now()
	d.recordBracketEvidence("guarded", protocol.StateWorking)
	d.recordPTYEvidence("guarded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "approval", At: now, Detail: "Action Required"})
	d.resolveAllSessions(now.Add(time.Second))
	if got := d.store.Get("guarded").State; got == protocol.SessionStatePendingApproval {
		t.Fatal("brief guardian-reviewed approval published after daemon recovery")
	}

	d.recordPTYEvidence("guarded", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now.Add(2 * time.Second)})
	d.resolveAllSessions(now.Add(3 * time.Second))
	if got := d.store.Get("guarded").State; got != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want working after guardian answered", got)
	}
}

func TestRecoveryUsesWorkerApprovalRouteAndRepairsStoredIntent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "route-mismatch", protocol.SessionStateWorking, time.Now().Add(-time.Minute))
	d.store.SetLaunchIntent("route-mismatch", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"route-mismatch"},
		info:    map[string]ptybackend.SessionInfo{"route-mismatch": runningInfo(nil)},
		params: map[string]ptybackend.SessionLaunchParams{
			"route-mismatch": {Recorded: true, ApprovalRoute: launchcontract.ApprovalRouteReviewer},
		},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if !evidenceOf(t, d, "route-mismatch").ReviewerInLoop {
		t.Fatal("stored route won over the surviving worker")
	}
	intent, ok := d.store.LaunchIntent("route-mismatch")
	if !ok || intent.ApprovalRoute != launchcontract.ApprovalRouteReviewer {
		t.Fatalf("repaired launch intent = %+v, ok=%v", intent, ok)
	}
}

func addRecoveredSession(t *testing.T, d *Daemon, id string, state protocol.SessionState, stateSince time.Time) {
	t.Helper()
	stamp := stateSince.UTC().Format(time.RFC3339Nano)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/" + id,
		State:          state,
		StateSince:     stamp,
		StateUpdatedAt: stamp,
		LastSeen:       stamp,
	})
}

func runningInfo(signal *pty.Observation) ptybackend.SessionInfo {
	info := ptybackend.SessionInfo{
		Running: true,
		Agent:   string(protocol.SessionAgentClaude),
		CWD:     "/tmp/recovered",
		State:   protocol.StateWorking,
	}
	if signal != nil {
		info.LastSignal = *signal
		info.HasLastSignal = true
	}
	return info
}

func TestReconcileKeepsPersistedStateOfLiveSessions(t *testing.T) {
	states := []protocol.SessionState{
		protocol.SessionStateIdle,
		protocol.SessionStateWorking,
		protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateUnknown,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			addRecoveredSession(t, d, "live", state, time.Now().Add(-time.Hour))
			d.ptyBackend = &fakeWorkerReconcileBackend{
				liveIDs: []string{"live"},
				info:    map[string]ptybackend.SessionInfo{"live": runningInfo(nil)},
			}

			report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

			if report.StateUpdated != 0 {
				t.Fatalf("state_updated = %d, want 0", report.StateUpdated)
			}
			if got := d.store.Get("live").State; got != state {
				t.Fatalf("recovered state = %q, want %q", got, state)
			}
		})
	}
}

func TestReconcileMarksExitedWorkerIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "dead", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	info := runningInfo(nil)
	info.Running = false
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"dead"},
		info:    map[string]ptybackend.SessionInfo{"dead": info},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if got := d.store.Get("dead").State; got != protocol.SessionStateIdle {
		t.Fatalf("recovered state = %q, want idle", got)
	}
}

func TestReconcileSeedsHeartbeatEvidenceFromWorker(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	observedAt := time.Now().Add(-2 * time.Minute)
	addRecoveredSession(t, d, "live", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	signal := heartbeat("not_busy", observedAt)
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live"},
		info:    map[string]ptybackend.SessionInfo{"live": runningInfo(&signal)},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	evidence, ok := d.evidenceTable().snapshot("live")
	if !ok {
		t.Fatal("no evidence recorded for the recovered session")
	}
	if evidence.Heartbeat == nil {
		t.Fatal("heartbeat evidence missing")
	}
	if evidence.Heartbeat.Claim != sessionstate.ClaimSettled {
		t.Fatalf("heartbeat claim = %q, want settled", evidence.Heartbeat.Claim)
	}
	if !evidence.Heartbeat.ObservedAt.Equal(observedAt.UTC()) && !evidence.Heartbeat.ObservedAt.Equal(observedAt) {
		t.Fatalf("heartbeat observed_at = %s, want %s", evidence.Heartbeat.ObservedAt, observedAt)
	}
}

func TestRecoveredSessionResolvesOffStaleWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "live", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	signal := heartbeat("not_busy", time.Now().Add(-30*time.Second))
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"live"},
		info:    map[string]ptybackend.SessionInfo{"live": runningInfo(&signal)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	d.resolveAllSessions(time.Now())

	if got := d.store.Get("live").State; got != protocol.SessionStateIdle {
		t.Fatalf("resolved state = %q, want idle", got)
	}
}

func TestRecoveredApprovalSurvivesTheResolver(t *testing.T) {
	for _, tc := range []struct {
		state protocol.SessionState
	}{
		{protocol.SessionStatePendingApproval},
		{protocol.SessionStateWaitingInput},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			concludedAt := time.Now().Add(-10 * time.Minute)
			addRecoveredSession(t, d, "blocked", tc.state, concludedAt)
			signal := heartbeat("not_busy", concludedAt.Add(-time.Second))
			d.ptyBackend = &fakeWorkerReconcileBackend{
				liveIDs: []string{"blocked"},
				info:    map[string]ptybackend.SessionInfo{"blocked": runningInfo(&signal)},
			}
			d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

			d.resolveAllSessions(time.Now())

			if got := d.store.Get("blocked").State; got != tc.state {
				t.Fatalf("resolved state = %q, want %q", got, tc.state)
			}
		})
	}
}

func TestRecoveredApprovalDropsWhenTheAgentMovedOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	concludedAt := time.Now().Add(-10 * time.Minute)
	addRecoveredSession(t, d, "answered", protocol.SessionStatePendingApproval, concludedAt)
	signal := heartbeat("not_busy", concludedAt.Add(time.Minute))
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"answered"},
		info:    map[string]ptybackend.SessionInfo{"answered": runningInfo(&signal)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	evidence, ok := d.evidenceTable().snapshot("answered")
	if !ok {
		t.Fatal("no evidence recorded for the recovered session")
	}
	if evidence.LastHarnessEvent != nil {
		t.Fatalf("harness edge restored for an agent that moved on: %+v", evidence.LastHarnessEvent)
	}

	d.resolveAllSessions(time.Now())

	if got := d.store.Get("answered").State; got != protocol.SessionStateIdle {
		t.Fatalf("resolved state = %q, want idle", got)
	}
}

func TestSnoozeWakeSurvivesDaemonRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "snoozed", protocol.SessionStateIdle, time.Now().Add(-time.Hour))
	deadline := time.Now().Add(time.Hour)
	if !d.store.SnoozeTurn("snoozed", deadline, time.Now()) {
		t.Fatal("failed to snooze the session")
	}
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"snoozed"},
		info:    map[string]ptybackend.SessionInfo{"snoozed": runningInfo(nil)},
	}
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if !attention.OpensTurn(d.store.Get("snoozed").State) {
		t.Fatalf("recovered state %q opens no turn, so the wake has nothing to deliver",
			d.store.Get("snoozed").State)
	}

	d.wakeSnooze("snoozed", deadline, "deadline")

	stamps := d.store.TurnStamps("snoozed")
	if !stamps.SnoozedUntil.IsZero() {
		t.Fatalf("snooze still armed after the wake: %s", stamps.SnoozedUntil)
	}
	if !stamps.OpenedAt.After(stamps.SettledAt) {
		t.Fatalf("wake opened no turn: opened=%s settled=%s", stamps.OpenedAt, stamps.SettledAt)
	}
}

func TestWorkerInfoClaimOnlyEndsLaunching(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial protocol.SessionState
		want    protocol.SessionState
	}{
		{"ends launching", protocol.SessionStateLaunching, protocol.SessionStateWorking},
		{"leaves idle alone", protocol.SessionStateIdle, protocol.SessionStateIdle},
		{"leaves pending approval alone", protocol.SessionStatePendingApproval, protocol.SessionStatePendingApproval},
		{"leaves unknown alone", protocol.SessionStateUnknown, protocol.SessionStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			addRecoveredSession(t, d, "s", tc.initial, time.Now().Add(-time.Hour))

			d.handlePTYState("s", pty.Observation{
				Source: pty.SourceWorkerInfo,
				Claim:  protocol.StateWorking,
				Detail: "watch subscribe replay",
				At:     time.Now(),
			})

			if got := d.store.Get("s").State; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}
