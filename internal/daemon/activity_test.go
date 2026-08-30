package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

func installActivityRunner(t *testing.T, d *Daemon) {
	t.Helper()
	runner := installQuietActivityRunner(t, d)
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
}

// Registered but not started: a caller that runs the queued job itself must be
// the only thing running it.
func installQuietActivityRunner(t *testing.T, d *Daemon) *jobs.Runner {
	t.Helper()
	d.store.SetSetting(SettingActivityEnabled, "true")
	d.store.SetSetting(SettingActivityConfig, `{"agent":"claude","model":"claude-haiku-4-5"}`)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))

	runner := jobs.New(jobs.Options{
		Store:        newTestJobStore(t, d),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWith(sessionActivityKind, d.sessionActivityHandler,
		jobs.HandlerConfig{Timeout: sessionActivityTimeout}); err != nil {
		t.Fatalf("register session_activity: %v", err)
	}
	d.jobQueue = runner
	return runner
}

func watchingClient(d *Daemon) {
	d.wsHub.clients[&wsClient{presence: clientPresence{
		Visible:          true,
		DashboardVisible: true,
		ReportedAt:       time.Now(),
	}}] = true
}

func addActivitySession(t *testing.T, d *Daemon, id string, state protocol.SessionState) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: state,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func writeActivityTranscript(t *testing.T, texts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendActivityTranscript(t, path, texts...)
	return path
}

func appendActivityTranscript(t *testing.T, path string, texts ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	for _, text := range texts {
		record, err := json.Marshal(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		})
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(record, '\n')); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
}

func TestActivityExecutorStoresTheGeneratedLine(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "I am going to run the frontend tests now")
	d.store.UpdateSessionActivity("session-1", "reading the plan", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "the suite is running")

	var seen agentdriver.HeadlessTaskRequest
	d.sessionActivityExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		seen = req
		return agentdriver.HeadlessTaskResult{Text: "running the frontend test suite."}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	stored := d.store.GetSessionActivity("session-1")
	if stored.Line != "running the frontend test suite" {
		t.Errorf("line = %q", stored.Line)
	}
	if stored.Cursor == "" {
		t.Error("cursor was not advanced, so the next run would re-read this window")
	}

	if !seen.DisableTools {
		t.Error("the run was allowed tools; an activity line has no business touching the filesystem")
	}
	if len(seen.OutputSchema) != 0 {
		t.Error("the run asked for a schema; the answer is the final text")
	}
	if strings.TrimSpace(seen.SystemPrompt) == "" {
		t.Error("SystemPrompt is empty, so the run pays the CLI's full system prefix")
	}
	if !strings.Contains(seen.Prompt, "the suite is running") {
		t.Errorf("prompt does not carry the new events:\n%s", seen.Prompt)
	}
	if !strings.Contains(seen.Prompt, "reading the plan") {
		t.Errorf("prompt does not carry the previous line:\n%s", seen.Prompt)
	}
	if !strings.Contains(seen.Prompt, string(protocol.SessionStateWorking)) {
		t.Errorf("prompt does not carry the session state:\n%s", seen.Prompt)
	}
}

func TestActivityExecutorSkipsAnUnmovedTranscript(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "already summarized")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))

	ran := false
	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{Text: "something else"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if ran {
		t.Error("an agent ran for a transcript that had not moved")
	}
	if got := d.store.GetSessionActivity("session-1").Line; got != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept", got)
	}
}

func TestActivityExecutorSeedsRatherThanScanningOnColdStart(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "ancient history", "more ancient history")

	ran := false
	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{Text: "reading ancient history"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if ran {
		t.Error("cold start ran an agent over the session's whole history")
	}
	stored := d.store.GetSessionActivity("session-1")
	if stored.Cursor == "" {
		t.Fatal("cold start did not seed a cursor, so the next run repeats it")
	}
	if stored.Line != "" {
		t.Errorf("cold start invented a line: %q", stored.Line)
	}
}

func TestActivityExecutorReseedsAfterTheTranscriptIsRewritten(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "before compaction", "still before")
	stale := seedActivityCursor(t, transcriptPath)
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), stale)

	if err := os.Remove(transcriptPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	appendActivityTranscript(t, transcriptPath, "after compaction")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		t.Error("an agent ran against a cursor the transcript no longer matches")
		return agentdriver.HeadlessTaskResult{}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	stored := d.store.GetSessionActivity("session-1")
	if stored.Cursor == stale || stored.Cursor == "" {
		t.Errorf("cursor = %q, want a fresh seed against the rewritten transcript", stored.Cursor)
	}
	if stored.Line != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept through a re-seed", stored.Line)
	}
}

func TestActivityExecutorKeepsThePreviousLineWhenNothingUsableCameBack(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Text: "   \n  "}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if got := d.store.GetSessionActivity("session-1").Line; got != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept", got)
	}
}

func TestActivityExecutorGeneratesNothingWhenAway(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)

	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		t.Error("an agent ran while nobody was looking")
		return agentdriver.HeadlessTaskResult{}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)
}

func TestEnqueueSessionActivityRefusesBeforeQueueing(t *testing.T) {
	newDaemon := func(t *testing.T) (*Daemon, string) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
		installActivityRunner(t, d)
		return d, "session-1"
	}

	t.Run("off", func(t *testing.T) {
		d, id := newDaemon(t)
		watchingClient(d)
		d.store.SetSetting(SettingActivityEnabled, "false")
		d.enqueueSessionActivity(id)
		assertNoActivityJob(t, d, id)
	})

	t.Run("away", func(t *testing.T) {
		d, id := newDaemon(t)
		d.enqueueSessionActivity(id)
		assertNoActivityJob(t, d, id)
	})

	t.Run("a shell split from an agent", func(t *testing.T) {
		d, _ := newDaemon(t)
		watchingClient(d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "shell-1", Label: "shell-1", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateWorking,
			ParentSessionID: protocol.Ptr("session-1"),
			StateSince:      now, StateUpdatedAt: now, LastSeen: now,
		})
		d.enqueueSessionActivity("shell-1")
		assertNoActivityJob(t, d, "shell-1")
	})
}

func TestActivityScanRespectsTheTierInterval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)
	d.store.SetSetting(SettingActivityIntervals, `{"watching":120,"present":300}`)

	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first", "second")
	generatedInsideWindow := time.Now().Add(-10 * time.Second)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedInsideWindow, "v1:abc:1:0")
	touchFile(t, transcriptPath, time.Now())

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")

	generatedAt := time.Now().Add(-5 * time.Minute)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedAt, "v1:abc:1:0")
	touchFile(t, transcriptPath, generatedAt.Add(time.Minute))
	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("nothing was queued past the interval (err=%v)", err)
	}
}

func TestActivityScanSkipsASessionThatHasNotWritten(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
	generatedAt := time.Now().Add(-time.Hour)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedAt, "v1:abc:1:0")
	touchFile(t, transcriptPath, generatedAt.Add(-time.Minute))

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")
}

func TestActivityScanHoldsAFailedRunToTheInterval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)
	d.store.SetSetting(SettingActivityIntervals, `{"watching":120,"present":300}`)

	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
	touchFile(t, transcriptPath, time.Now())
	d.noteSessionActivityRun("session-1", func(run *sessionActivityRun) {
		run.ObservedAt = time.Now().Add(-10 * time.Second)
		run.SpentAt = time.Now().Add(-10 * time.Second)
	})

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")

	d.noteSessionActivityRun("session-1", func(run *sessionActivityRun) {
		run.ObservedAt = time.Now().Add(-10 * time.Minute)
		run.SpentAt = time.Now().Add(-10 * time.Minute)
	})
	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("nothing was queued past the interval (err=%v)", err)
	}
}

func TestActivityScanTreatsASpendlessPassAsHavingLooked(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
	looked := time.Now()
	touchFile(t, transcriptPath, looked.Add(-time.Minute))
	d.noteSessionActivityRun("session-1", func(run *sessionActivityRun) { run.ObservedAt = looked })

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")

	touchFile(t, transcriptPath, looked.Add(time.Minute))
	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("a session that wrote after the seed was not queued (err=%v)", err)
	}
}

func TestActivityTranscriptPathIsRememberedUntilItMoves(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	session := d.store.Get("session-1")

	transcriptPath := discoverableTranscript(t, d, "session-1", "session-1", "first")
	if got := d.sessionActivityTranscript(session); got != transcriptPath {
		t.Fatalf("resolved %q, want %q", got, transcriptPath)
	}

	t.Setenv(toolhome.EnvVar, t.TempDir())
	if got := d.sessionActivityTranscript(session); got != transcriptPath {
		t.Errorf("resolved %q after the tool home moved, want the remembered %q", got, transcriptPath)
	}

	d.store.SetResumeSessionID("session-1", "resume-2")
	if got := d.sessionActivityTranscript(session); got != "" {
		t.Errorf("resolved %q after the session resumed, want a fresh resolve (which finds nothing here)", got)
	}

	restored := discoverableTranscript(t, d, "session-1", "resume-2", "first")
	if got := d.sessionActivityTranscript(session); got != restored {
		t.Fatalf("resolved %q, want %q", got, restored)
	}
	if err := os.Remove(restored); err != nil {
		t.Fatal(err)
	}
	if got := d.sessionActivityTranscript(session); got != "" {
		t.Errorf("resolved %q after the transcript was removed, want a fresh resolve", got)
	}
}

func TestActivityExecutorWritesNothingAfterTheFeatureIsTurnedOff(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		d.store.SetSetting(SettingActivityEnabled, "false")
		return agentdriver.HeadlessTaskResult{Text: "running the frontend test suite"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if got := d.store.GetSessionActivity("session-1").Line; got != "" {
		t.Errorf("line = %q, written after the feature was turned off", got)
	}
}

func TestActivityExecutorWritesNothingAfterTheConversationChanges(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	d.store.SetResumeSessionID("session-1", "conversation-old")
	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "reading the plan", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		changed, err := d.store.TransitionSessionConversation("session-1", "conversation-new", "/transcripts/conversation-new.jsonl")
		if err != nil || !changed {
			t.Fatalf("transition during activity generation: changed=%v err=%v", changed, err)
		}
		return agentdriver.HeadlessTaskResult{Text: "summarizing the old conversation"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if got := d.store.GetResumeSessionID("session-1"); got != "conversation-new" {
		t.Fatalf("resume id = %q, want successor conversation", got)
	}
	if got := d.store.GetSessionActivity("session-1"); got != (store.SessionActivity{}) {
		t.Errorf("activity = %+v after transition, want the atomic clear preserved", got)
	}
}

func TestActivityStatusReportsWhyASessionStoppedMoving(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	d.store.SetSetting(SettingActivityEnabled, "true")
	d.store.SetSetting(SettingActivityConfig, `{"agent":"claude","model":"claude-haiku-4-5"}`)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), "v1:abc:1:0")
	d.noteSessionActivityRun("session-1", func(run *sessionActivityRun) { run.Err = "claude exited 1: not logged in" })
	watchingClient(d)

	resp := socketRoundTrip(t, d, protocol.ActivityStatusMessage{Cmd: protocol.CmdActivityStatus})
	if !resp.Ok || resp.ActivityStatusResult == nil || len(resp.ActivityStatusResult.Sessions) != 1 {
		t.Fatalf("activity_status = %+v", resp)
	}
	got := resp.ActivityStatusResult.Sessions[0]
	if got.Error == nil || !strings.Contains(*got.Error, "not logged in") {
		t.Errorf("session error = %v, want the failure that stopped it", got.Error)
	}
	if protocol.Deref(got.Activity) != "running the test suite" {
		t.Errorf("activity = %q, want the last good line kept beside the failure", protocol.Deref(got.Activity))
	}
}

func TestActivityScanGeneratesNothingWhenAway(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)

	discoverableTranscript(t, d, "session-1", "session-1", "first")

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")
}

func TestOpeningATurnRefreshesTheActivityLineImmediately(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	discoverableTranscript(t, d, "session-1", "session-1", "first")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), "v1:abc:1:0")

	if !d.applyState(sessionStateChange{
		sessionID: "session-1",
		state:     string(protocol.SessionStateWaitingInput),
		cause:     liveSignal{},
	}) {
		t.Fatal("state was not applied")
	}

	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("opening a turn did not queue a refresh (err=%v)", err)
	}
}

func TestSessionGeneratesActivity(t *testing.T) {
	agentSession := &protocol.Session{ID: "s1", Agent: protocol.SessionAgentClaude}
	if !sessionGeneratesActivity(agentSession) {
		t.Error("a plain agent session does not generate activity")
	}
	satellite := &protocol.Session{ID: "s2", Agent: protocol.SessionAgentClaude, ParentSessionID: protocol.Ptr("s1")}
	if sessionGeneratesActivity(satellite) {
		t.Error("a satellite shell generates activity; it has no transcript of its own")
	}
	remote := &protocol.Session{ID: "s3", Agent: protocol.SessionAgentClaude, EndpointID: protocol.Ptr("remote-1")}
	if sessionGeneratesActivity(remote) {
		t.Error("a remote session generates activity; its transcript lives on another daemon")
	}
	if sessionGeneratesActivity(nil) {
		t.Error("a nil session generates activity")
	}
}

func enqueueActivity(t *testing.T, d *Daemon, sessionID, transcriptPath string) {
	t.Helper()
	if _, err := d.jobQueue.Enqueue(sessionActivityKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Payload: sessionActivityPayload{
			Transcript: transcriptPath,
			ResumeID:   d.store.GetResumeSessionID(sessionID),
		},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func assertNoActivityJob(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	job, err := d.jobQueue.GetByKey(sessionActivityKind, sessionID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job != nil {
		t.Fatalf("a job was queued anyway: %+v", job)
	}
}

func discoverableTranscript(t *testing.T, d *Daemon, sessionID, nativeID string, texts ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	dir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	path := filepath.Join(dir, nativeID+".jsonl")
	appendActivityTranscript(t, path, texts...)
	if changed, err := d.store.TransitionSessionConversation(sessionID, nativeID, path); err != nil || !changed {
		t.Fatalf("bind transcript: changed=%v err=%v", changed, err)
	}
	return path
}

func touchFile(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func seedActivityCursor(t *testing.T, path string) string {
	t.Helper()
	cursor, err := transcript.HeadCursor(path)
	if err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
	return cursor
}

func socketRoundTrip(t *testing.T, d *Daemon, msg any) protocol.Response {
	t.Helper()
	server, conn := net.Pipe()
	defer conn.Close()
	go d.handleConnection(server)
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp protocol.Response
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestActivityStatusReportsTheTierAndEveryLine(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	d.store.SetSetting(SettingActivityEnabled, "true")
	d.store.SetSetting(SettingActivityConfig, `{"agent":"claude","model":"claude-haiku-4-5"}`)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))
	d.store.UpdateSessionActivity("session-1", "running the frontend test suite", time.Now(), "v1:abc:1:0")
	watchingClient(d)

	resp := socketRoundTrip(t, d, protocol.ActivityStatusMessage{Cmd: protocol.CmdActivityStatus})
	if !resp.Ok || resp.ActivityStatusResult == nil {
		t.Fatalf("activity_status failed: %+v", resp)
	}
	status := resp.ActivityStatusResult
	if status.PresenceTier != "watching" {
		t.Errorf("presence_tier = %q, want watching", status.PresenceTier)
	}
	if !status.Enabled {
		t.Error("enabled = false with the setting on")
	}
	if status.Error != nil {
		t.Errorf("error = %q on a runnable configuration", *status.Error)
	}
	if len(status.Sessions) != 1 || protocol.Deref(status.Sessions[0].Activity) != "running the frontend test suite" {
		t.Fatalf("sessions = %+v", status.Sessions)
	}
}

func TestActivityStatusNamesAnUnfinishedSetup(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingActivityEnabled, "true")

	resp := socketRoundTrip(t, d, protocol.ActivityStatusMessage{Cmd: protocol.CmdActivityStatus})
	if resp.ActivityStatusResult == nil || resp.ActivityStatusResult.Error == nil {
		t.Fatalf("no error reported for an enabled feature with no agent: %+v", resp.ActivityStatusResult)
	}
	if !strings.Contains(*resp.ActivityStatusResult.Error, "agent") {
		t.Errorf("error = %q, want it to name the missing agent", *resp.ActivityStatusResult.Error)
	}
}

func TestClearSessionActivityForgetsTheLineAndTheCursor(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateIdle)
	d.store.UpdateSessionActivity("session-1", "wrong line", time.Now(), "v1:abc:1:0")

	resp := socketRoundTrip(t, d, protocol.ClearSessionActivityMessage{
		Cmd: protocol.CmdClearSessionActivity, ID: "session-1",
	})
	if !resp.Ok {
		t.Fatalf("clear failed: %+v", resp)
	}
	if got := d.store.GetSessionActivity("session-1"); got != (store.SessionActivity{}) {
		t.Errorf("activity = %+v after a clear, want nothing", got)
	}
}

func TestDisablingActivityClearsEveryLine(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	addActivitySession(t, d, "session-2", protocol.SessionStateIdle)
	d.store.SetSetting(SettingActivityEnabled, "true")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), "v1:abc:1:0")
	d.store.UpdateSessionActivity("session-2", "reading the plan", time.Now(), "v1:abc:1:0")

	d.handleSetSettingWS(&wsClient{}, &protocol.SetSettingMessage{
		Cmd: protocol.CmdSetSetting, Key: SettingActivityEnabled, Value: "false",
	})

	for _, id := range []string{"session-1", "session-2"} {
		if got := d.store.GetSessionActivity(id).Line; got != "" {
			t.Errorf("%s still reads %q after the feature was turned off", id, got)
		}
	}
}
