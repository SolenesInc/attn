package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

func installActivityRunner(t *testing.T, d *Daemon) {
	t.Helper()
	runner := installQuietActivityRunner(t, d)
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
}

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

func writeFakeAgentExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return path
}
