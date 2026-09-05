package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessioninstructions"
	"github.com/victorarias/attn/internal/store"
)

func newHeadlessDaemon(t *testing.T) (*Daemon, func() string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger

	return d, func() string {
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read daemon log: %v", err)
		}
		return string(body)
	}
}

func requireRefusal(t *testing.T, readLog func() string, caller string) {
	t.Helper()
	want := "headless task refused (" + caller + ")"
	if log := readLog(); !strings.Contains(log, want) {
		t.Fatalf("daemon log missing %q, got:\n%s", want, log)
	}
}

func TestHeadlessSwitchOffRefusesTheStopClassifier(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	fake := NewFakeClassifier(protocol.StateWaitingInput)
	d.classifier = fake

	state, err := d.runClassifier(nil, "did that work?", time.Second)

	if !errors.Is(err, headless.ErrRefused) {
		t.Fatalf("runClassifier error = %v, want ErrRefused", err)
	}
	if state != protocol.StateUnknown {
		t.Fatalf("state = %q, want %q", state, protocol.StateUnknown)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("classifier ran %d times, want 0", len(calls))
	}
	requireRefusal(t, readLog, "classifier")
}

func TestHeadlessSwitchOffStillSettlesASession(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, _ := newHeadlessDaemon(t)
	fake := NewFakeClassifier(protocol.StateWaitingInput)
	d.classifier = fake

	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             "sess-refused",
		Agent:          protocol.SessionAgentCodex,
		Label:          "test",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Now running pre-review."}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d.recordBracketEvidence("sess-refused", protocol.StateWorking)
	d.recordPTYEvidence("sess-refused", pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence("sess-refused", protocol.StateIdle)
	d.recordPTYEvidence("sess-refused", pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})

	d.classifySessionState("sess-refused", transcriptPath)
	d.resolveAllSessions(time.Now())

	session := d.store.Get("sess-refused")
	if session == nil {
		t.Fatal("session missing after classify")
	}
	if session.State != protocol.StateIdle {
		t.Fatalf("state = %s, want %s: a refused classifier must not strand the turn", session.State, protocol.StateIdle)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("classifier ran %d times, want 0", len(calls))
	}
}

func TestHeadlessSwitchOffRefusesSessionTitle(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	directory := t.TempDir()
	seedSessionTitleSession(t, d, "sess-1", directory, "")
	transcriptPath := writeSessionTitleTranscript(t)

	calls := 0
	d.sessionTitleExec = func(context.Context, *protocol.Session, string) (string, error) {
		calls++
		return "Fix login flow", nil
	}
	installSessionTitleRunner(t, d)

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)
	runSessionTitleJobs(t, d)

	if calls != 0 {
		t.Fatalf("title exec ran %d times, want 0", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != defaultSessionLabel(directory, "sess-1") {
		t.Fatalf("session label = %+v, want the launch default", got)
	}
	requireRefusal(t, readLog, "session_title")
}

func TestHeadlessSwitchOffRefusesTicketReconcile(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	sessionID := delegateBoundSession(t, d)
	ticketID := boundTicketID(t, d, sessionID)
	_, calls := armReconcileObserver(d, agentdriver.HeadlessTaskResult{}, nil)
	installReconcileRunner(t, d)

	d.reconcileTicketsOnSessionEnd(sessionID, protocol.StateIdle)

	if job, err := d.jobQueue.GetByKey(reconcileKind, ticketID); err != nil || job != nil {
		t.Fatalf("a refused duty queued reconcile for %s (job=%v err=%v)", ticketID, job, err)
	}
	if *calls != 0 {
		t.Fatalf("reconcile classifier ran %d times, want 0", *calls)
	}
	requireRefusal(t, readLog, reconcileKind)
}

func TestHeadlessSwitchOffRefusesSessionInstructions(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)

	_, err := daemonSessionInstructionsModel{daemon: d}.Run(
		context.Background(),
		sessioninstructions.ModelRequest{Question: "what did it do?"},
	)

	if !errors.Is(err, headless.ErrRefused) {
		t.Fatalf("Run() error = %v, want ErrRefused", err)
	}
	requireRefusal(t, readLog, "session_instructions")
}

func TestHeadlessTasksSettingMirrorsIntoTheSwitchAndEnvWins(t *testing.T) {
	d, readLog := newHeadlessDaemon(t)
	t.Cleanup(func() { headless.SetStoredEnabled(true) })

	d.store.SetSetting(SettingHeadlessTasksEnabled, "false")
	d.applyHeadlessTasksMode()
	if headless.Enabled() {
		t.Fatal("the stored setting did not reach the switch")
	}
	if log := readLog(); !strings.Contains(log, "headless tasks: off ("+headless.SettingKey+")") {
		t.Fatalf("startup line missing the setting source, got:\n%s", log)
	}

	t.Setenv(headless.EnvVar, "on")
	if !headless.Enabled() {
		t.Fatal("the env override did not win over the stored setting")
	}

	d.store.SetSetting(SettingHeadlessTasksEnabled, "true")
	d.applyHeadlessTasksMode()
	os.Unsetenv(headless.EnvVar)
	if !headless.Enabled() {
		t.Fatal("clearing the setting did not turn headless tasks back on")
	}
}

func countRefusals(log, caller string) int {
	return strings.Count(log, "headless task refused ("+caller+")")
}

func TestHeadlessRefusalFollowsTheWork(t *testing.T) {
	t.Run("session_activity/a_fresh_session_only_seeds_its_cursor", func(t *testing.T) {
		t.Setenv(headless.EnvVar, "off")
		d, readLog := newHeadlessDaemon(t)
		installQuietActivityRunner(t, d)
		watchingClient(d)
		addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
		transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
		modelCalls := countActivityModelCalls(d)

		scanAndRunActivity(t, d, "session-1", transcriptPath, 3)

		if cursor := d.store.GetSessionActivity("session-1").Cursor; cursor == "" {
			t.Fatal("the cursor was never seeded, so every scan keeps finding the session due")
		}
		if *modelCalls != 0 {
			t.Fatalf("the activity model ran %d times with the switch off, want 0", *modelCalls)
		}
		if n := countRefusals(readLog(), sessionActivityKind); n != 0 {
			t.Fatalf("cursor maintenance refused %d times, want 0", n)
		}
	})

	t.Run("session_activity/one_delta_refuses_once", func(t *testing.T) {
		t.Setenv(headless.EnvVar, "off")
		d, readLog := newHeadlessDaemon(t)
		installQuietActivityRunner(t, d)
		watchingClient(d)
		addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
		transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
		seedActivityCursorFor(t, d, "session-1", transcriptPath)
		appendActivityTranscript(t, transcriptPath, "second")
		modelCalls := countActivityModelCalls(d)

		scanAndRunActivity(t, d, "session-1", transcriptPath, 3)

		if *modelCalls != 0 {
			t.Fatalf("the activity model ran %d times with the switch off, want 0", *modelCalls)
		}
		if n := countRefusals(readLog(), sessionActivityKind); n != 1 {
			t.Fatalf("the same delta refused %d times, want 1", n)
		}
	})

	cases := []struct {
		name   string
		caller string
		idle   func(*testing.T, *Daemon)
		work   func(*testing.T, *Daemon)
	}{
		{
			name:   "session_title",
			caller: "session_title",
			idle: func(t *testing.T, d *Daemon) {
				seedSessionTitleSession(t, d, "sess-1", t.TempDir(), "a name the user already chose")
				d.sessionTitleExec = refusingTitleExec(t)
				d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
			},
			work: func(t *testing.T, d *Daemon) {
				seedSessionTitleSession(t, d, "sess-1", t.TempDir(), "")
				d.sessionTitleExec = refusingTitleExec(t)
				d.maybeGenerateSessionTitle("sess-1", writeSessionTitleTranscript(t))
			},
		},
		{
			name:   "reconcile_sweep",
			caller: reconcileKind,
			idle: func(t *testing.T, d *Daemon) {
				installReconcileRunner(t, d)
				d.ticketReconcileSweepPass(time.Now())
			},
			work: func(t *testing.T, d *Daemon) {
				installReconcileRunner(t, d)
				if _, err := d.store.CreateTicket(store.Ticket{
					ID: "orphaned", Title: "t", Assignee: "sess-dead", Status: store.TicketStatusInReview,
				}, "chief", time.Now().Add(-time.Hour)); err != nil {
					t.Fatalf("CreateTicket: %v", err)
				}
				t0 := time.Now()
				d.ticketReconcileSweepPass(t0)
				d.ticketReconcileSweepPass(t0.Add(ticketReconcileGrace() + time.Minute))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/idle", func(t *testing.T) {
			t.Setenv(headless.EnvVar, "off")
			d, readLog := newHeadlessDaemon(t)
			tc.idle(t, d)
			if log := readLog(); countRefusals(log, tc.caller) != 0 {
				t.Fatalf("an idle daemon refused %s %d times, want 0:\n%s",
					tc.caller, countRefusals(log, tc.caller), log)
			}
		})
		t.Run(tc.name+"/work", func(t *testing.T) {
			t.Setenv(headless.EnvVar, "off")
			d, readLog := newHeadlessDaemon(t)
			tc.work(t, d)
			if log := readLog(); countRefusals(log, tc.caller) != 1 {
				t.Fatalf("a daemon with work refused %s %d times, want 1:\n%s",
					tc.caller, countRefusals(log, tc.caller), log)
			}
		})
	}
}

// The future mtime is what makes the next scan find the session due again;
// nothing here advances the wall clock.
func scanAndRunActivity(t *testing.T, d *Daemon, sessionID, transcriptPath string, passes int) {
	t.Helper()
	for pass := 0; pass < passes; pass++ {
		touchFile(t, transcriptPath, time.Now().Add(time.Minute))
		if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
			t.Fatalf("session activity scan: %v", err)
		}
		runQueuedActivityJob(t, d, sessionID)
	}
}

// The row is dropped after the run so the next pass proves that scan queued a
// job, rather than finding this one still sitting there.
func runQueuedActivityJob(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	job, err := d.jobQueue.GetByKey(sessionActivityKind, sessionID)
	if err != nil {
		t.Fatalf("look up activity job: %v", err)
	}
	if job == nil {
		t.Fatalf("the scan queued no activity job for %s", sessionID)
	}
	if _, err := d.sessionActivityHandler(context.Background(), job); err != nil {
		t.Fatalf("session activity job: %v", err)
	}
	d.jobQueue.RemoveByKey(sessionActivityKind, sessionID)
}

func countActivityModelCalls(d *Daemon) *int {
	calls := 0
	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		calls++
		return agentdriver.HeadlessTaskResult{Text: "running the test suite."}, nil
	}
	return &calls
}

func seedActivityCursorFor(t *testing.T, d *Daemon, sessionID, transcriptPath string) {
	t.Helper()
	resumeID := d.store.GetResumeSessionID(sessionID)
	if !d.store.SetSessionActivityCursorForConversation(sessionID, resumeID, seedActivityCursor(t, transcriptPath)) {
		t.Fatalf("seed activity cursor for %s", sessionID)
	}
}

// The error is discarded on purpose: a handler that bails on ineligibility is
// exactly the idle case, and the refusal count is the assertion.
func refusingTitleExec(t *testing.T) func(context.Context, *protocol.Session, string) (string, error) {
	t.Helper()
	return func(context.Context, *protocol.Session, string) (string, error) {
		t.Error("a refused duty called the title model")
		return "", nil
	}
}

func TestSettingsSnapshotSeparatesStoredHeadlessValueFromTheEffectiveOne(t *testing.T) {
	d, _ := newHeadlessDaemon(t)
	t.Cleanup(func() { headless.SetStoredEnabled(true) })

	d.store.SetSetting(SettingHeadlessTasksEnabled, "true")
	d.applyHeadlessTasksMode()
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingHeadlessTasksEnabled]; got != "true" {
		t.Fatalf("effective = %v, want true", got)
	}
	if got := settings[SettingHeadlessTasksEnabledStored]; got != "true" {
		t.Fatalf("stored = %v, want true", got)
	}
	if raw, ok := settings[SettingHeadlessTasksEnabledOverride]; ok {
		t.Fatalf("override = %v, want it absent with no env var set", raw)
	}

	t.Setenv(headless.EnvVar, "off")
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingHeadlessTasksEnabled]; got != "false" {
		t.Fatalf("effective = %v, want false under the env override", got)
	}
	if got := settings[SettingHeadlessTasksEnabledStored]; got != "true" {
		t.Fatalf("stored = %v, want the env override to leave it alone", got)
	}
	if got := settings[SettingHeadlessTasksEnabledOverride]; got != "off" {
		t.Fatalf("override = %v, want off", got)
	}
}
