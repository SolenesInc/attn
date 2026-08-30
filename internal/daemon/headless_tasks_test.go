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
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessioninstructions"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
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
	d.sessionTitleExec = func(context.Context, *protocol.Session, transcript.ConversationSlice) (string, error) {
		calls++
		return "Fix login flow", nil
	}

	d.maybeGenerateSessionTitle("sess-1", transcriptPath)

	if calls != 0 {
		t.Fatalf("title exec ran %d times, want 0", calls)
	}
	if got := d.store.Get("sess-1"); got == nil || got.Label != defaultSessionLabel(directory, "sess-1") {
		t.Fatalf("session label = %+v, want the launch default", got)
	}
	requireRefusal(t, readLog, "session_title")
}

func TestHeadlessSwitchOffRefusesNotebookNarration(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		key   string
		drive func(*Daemon)
	}{
		{
			name:  "summarize_session",
			kind:  notebookSummarizeSessionKind,
			key:   "sess-1",
			drive: func(d *Daemon) { d.enqueueSummarizeSession("sess-1", "", "workspace-1") },
		},
		{
			name:  "narrate_workspace",
			kind:  notebookNarrateWorkspaceKind,
			key:   "workspace-1",
			drive: func(d *Daemon) { d.enqueueNarrateWorkspace("workspace-1") },
		},
		{
			name:  "narrate_workspace_daily",
			kind:  notebookNarrateWorkspaceKind,
			key:   "workspace-1",
			drive: func(d *Daemon) { d.enqueueDailyNarrateWorkspace("workspace-1") },
		},
		{
			name:  "narrate_workspace_final",
			kind:  notebookNarrateWorkspaceKind,
			key:   "workspace-1",
			drive: func(d *Daemon) { d.enqueueFinalNarrateWorkspace("workspace-1") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/control", func(t *testing.T) {
			t.Setenv(headless.EnvVar, "on")
			d, _ := newHeadlessDaemon(t)
			installNotebookNarrationRunner(t, d)
			tc.drive(d)
			if job, err := d.jobQueue.GetByKey(tc.kind, tc.key); err != nil || job == nil {
				t.Fatalf("switch on queued nothing (job=%v err=%v)", job, err)
			}
		})
		t.Run(tc.name+"/refused", func(t *testing.T) {
			t.Setenv(headless.EnvVar, "off")
			d, readLog := newHeadlessDaemon(t)
			installNotebookNarrationRunner(t, d)
			tc.drive(d)
			job, err := d.jobQueue.GetByKey(tc.kind, tc.key)
			if err != nil {
				t.Fatalf("lookup job: %v", err)
			}
			if job != nil {
				t.Fatalf("a refused duty queued %s/%s", tc.kind, tc.key)
			}
			requireRefusal(t, readLog, tc.kind)
		})
	}
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

func TestHeadlessSwitchOffRefusesSessionActivity(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)
	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first", "second")
	touchFile(t, transcriptPath, time.Now())

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")

	d.enqueueSessionActivity("session-1")
	assertNoActivityJob(t, d, "session-1")
	requireRefusal(t, readLog, sessionActivityKind)
}

func TestHeadlessSwitchOffRefusesKeeperCompact(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = 1
	if !d.jobQueue.Disabled() {
		t.Fatal("expected a disabled runner, so the inline compaction path is the one under test")
	}
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)

	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	canonical, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get canonical: %v", err)
	}
	d.enqueueWorkspaceContextCompaction(canonical)

	current, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get current context: %v", err)
	}
	if current.Revision != 1 || current.Content != keeperSource {
		t.Fatalf("a refused duty compacted the context anyway: %+v", current)
	}
	requireRefusal(t, readLog, compactContextKind)
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

// The receipt the reviewer asked for: with the switch off, an idle daemon must
// stay silent, and a daemon with work must say so exactly once per run.
func TestHeadlessRefusalFollowsTheWork(t *testing.T) {
	cases := []struct {
		name   string
		caller string
		idle   func(*testing.T, *Daemon)
		work   func(*testing.T, *Daemon)
	}{
		{
			name:   "session_activity_scan",
			caller: sessionActivityKind,
			idle: func(t *testing.T, d *Daemon) {
				installActivityRunner(t, d)
				watchingClient(d)
				runActivityScan(t, d)
			},
			work: func(t *testing.T, d *Daemon) {
				installActivityRunner(t, d)
				watchingClient(d)
				addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
				touchFile(t, discoverableTranscript(t, d, "session-1", "session-1", "first"), time.Now())
				runActivityScan(t, d)
			},
		},
		{
			name:   "session_activity_job",
			caller: sessionActivityKind,
			idle: func(t *testing.T, d *Daemon) {
				installActivityRunner(t, d)
				watchingClient(d)
				runActivityJob(t, d, "session-gone")
			},
			work: func(t *testing.T, d *Daemon) {
				installActivityRunner(t, d)
				watchingClient(d)
				addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
				touchFile(t, discoverableTranscript(t, d, "session-1", "session-1", "first"), time.Now())
				runActivityJob(t, d, "session-1")
			},
		},
		{
			name:   "compact_context",
			caller: compactContextKind,
			idle: func(t *testing.T, d *Daemon) {
				compactWorkspaceOfSize(t, d, 1<<20)
			},
			work: func(t *testing.T, d *Daemon) {
				compactWorkspaceOfSize(t, d, 1)
			},
		},
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
		{
			name:   "summarize_session",
			caller: notebookSummarizeSessionKind,
			idle: func(t *testing.T, d *Daemon) {
				installNotebookNarrationRunner(t, d)
				runNotebookJob(t, d, d.summarizeSessionHandler, notebookSummarizeSessionKind, "sess-gone")
			},
			work: func(t *testing.T, d *Daemon) {
				installNotebookNarrationRunner(t, d)
				addActivitySession(t, d, "sess-1", protocol.SessionStateWorking)
				runNotebookJob(t, d, d.summarizeSessionHandler, notebookSummarizeSessionKind, "sess-1")
			},
		},
		{
			name:   "narrate_workspace",
			caller: notebookNarrateWorkspaceKind,
			idle: func(t *testing.T, d *Daemon) {
				installNotebookNarrationRunner(t, d)
				runNotebookJob(t, d, d.narrateWorkspaceHandler, notebookNarrateWorkspaceKind, "")
			},
			work: func(t *testing.T, d *Daemon) {
				installNotebookNarrationRunner(t, d)
				runNotebookJob(t, d, d.narrateWorkspaceHandler, notebookNarrateWorkspaceKind, "workspace-1")
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

func runActivityScan(t *testing.T, d *Daemon) {
	t.Helper()
	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("session activity scan: %v", err)
	}
}

func runActivityJob(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	job := &jobs.Job{Kind: sessionActivityKind, UniqueKey: sessionID}
	if _, err := d.sessionActivityHandler(context.Background(), job); err != nil {
		t.Fatalf("session activity job: %v", err)
	}
}

// The error is discarded on purpose: a handler that bails on ineligibility is
// exactly the idle case, and the refusal count is the assertion.
func runNotebookJob(t *testing.T, d *Daemon, handler func(context.Context, *jobs.Job) (any, error), kind, subject string) {
	t.Helper()
	_, _ = handler(context.Background(), &jobs.Job{Kind: kind, UniqueKey: subject})
}

func refusingTitleExec(t *testing.T) func(context.Context, *protocol.Session, transcript.ConversationSlice) (string, error) {
	t.Helper()
	return func(context.Context, *protocol.Session, transcript.ConversationSlice) (string, error) {
		t.Error("a refused duty called the title model")
		return "", nil
	}
}

func compactWorkspaceOfSize(t *testing.T, d *Daemon, threshold int) {
	t.Helper()
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")
	d.store.SetSetting(SettingKeeperCompact, `{"agent":"codex","model":"gpt-test"}`)
	d.keeperCompactThreshold = threshold
	d.workspaceContextCompactionExecution = fakeCompaction(keeperCandidate)
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-1", keeperSource, "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	canonical, err := d.store.GetWorkspaceContext("workspace-1")
	if err != nil {
		t.Fatalf("get canonical: %v", err)
	}
	d.enqueueWorkspaceContextCompaction(canonical)
}
