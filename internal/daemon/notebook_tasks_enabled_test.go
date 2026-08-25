package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
)

func TestNotebookTasksEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookTasksEnabled() {
		t.Fatal("unset notebook.tasks_enabled must default to ON")
	}
	for _, on := range []string{"true", "on", "1", "yes", "  TRUE  "} {
		d.store.SetSetting(SettingNotebookTasksEnabled, on)
		if !d.notebookTasksEnabled() {
			t.Fatalf("value %q must enable keeper tasks", on)
		}
	}
	for _, off := range []string{"false", "off", "0", "no"} {
		d.store.SetSetting(SettingNotebookTasksEnabled, off)
		if d.notebookTasksEnabled() {
			t.Fatalf("value %q must disable keeper tasks", off)
		}
	}
}

func TestNotebookSummariesEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookSummariesEnabled() {
		t.Fatal("unset notebook.summarize_session.enabled must default to ON")
	}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookSummarizeSessionEnabled]; got != "true" {
		t.Fatalf("effective summary setting = %#v, want true", got)
	}

	d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")
	if d.notebookSummariesEnabled() {
		t.Fatal("explicit false must disable session summaries")
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookSummarizeSessionEnabled]; got != "false" {
		t.Fatalf("effective summary setting = %#v, want false", got)
	}
}

func TestNotebookWorkspaceNarrationEnabledDefaultsOn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	if !d.notebookWorkspaceNarrationEnabled() {
		t.Fatal("unset notebook.narrate_workspace.enabled must default to ON")
	}
	settings := d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookNarrateWorkspaceEnabled]; got != "true" {
		t.Fatalf("effective narration setting = %#v, want true", got)
	}

	d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")
	if d.notebookWorkspaceNarrationEnabled() {
		t.Fatal("explicit false must disable workspace narration")
	}
	settings = d.settingsWithAgentAvailability()
	if got := settings[SettingNotebookNarrateWorkspaceEnabled]; got != "false" {
		t.Fatalf("effective narration setting = %#v, want false", got)
	}
}

func TestNotebookSummariesDisabledOnlySkipsSummaries(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")
		d.enqueueSummarizeSession("session-off", "", "")
		d.enqueueNarrateWorkspace("ws-on")
		assertNoTask(t, d, notebookSummarizeSessionKind, "session-off")
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-on") {
			t.Fatal("summary switch must not disable journal narration")
		}

		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "true")
		d.enqueueSummarizeSession("session-on", "", "")
		if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
			t.Fatal("summarize must enqueue once its duty switch is on")
		}
	})
}

func TestNotebookNarrationDisabledOnlySkipsNarration(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		d.store.SetSetting(SettingNotebookNarrateWorkspace, `{"agent":"claude","model":"claude-custom"}`)
		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")
		d.enqueueNarrateWorkspace("ws-routine-off")
		d.enqueueDailyNarrateWorkspace("ws-daily-off")
		d.enqueueFinalNarrateWorkspace("ws-final-off")
		d.enqueueSummarizeSession("session-on", "", "")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-routine-off")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-daily-off")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-final-off")
		if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
			t.Fatal("narration switch must not disable session summaries")
		}

		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "true")
		d.enqueueNarrateWorkspace("ws-routine-on")
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-routine-on") {
			t.Fatal("narration must enqueue once its duty switch is on")
		}
		if got := d.store.GetSetting(SettingNotebookNarrateWorkspace); got != `{"agent":"claude","model":"claude-custom"}` {
			t.Fatalf("narration model config changed across toggle: %s", got)
		}
	})
}

func TestNotebookTasksDisabledSkipsEnqueue(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)

		d.store.SetSetting(SettingNotebookTasksEnabled, "false")
		d.enqueueSummarizeSession("session-off", "", "")
		d.enqueueNarrateWorkspace("ws-off")
		// The gate returns synchronously, so an immediate Get is authoritative.
		assertNoTask(t, d, notebookSummarizeSessionKind, "session-off")
		assertNoTask(t, d, notebookNarrateWorkspaceKind, "ws-off")

		d.store.SetSetting(SettingNotebookTasksEnabled, "true")
		d.enqueueSummarizeSession("session-on", "", "")
		d.enqueueNarrateWorkspace("ws-on")
		if !taskExists(t, d, notebookSummarizeSessionKind, "session-on") {
			t.Fatal("summarize must enqueue once the master switch is on")
		}
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-on") {
			t.Fatal("narrate must enqueue once the master switch is on")
		}
	})
}

func TestNotebookTasksDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookTasksEnabled, "false")

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("summarize executor ran the agent while the master switch was off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)
	})
}

func TestNotebookSummariesDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookSummarizeSessionEnabled, "false")

		d.summarizeSessionExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("summarize executor ran the agent while session summaries were off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookSummarizeSessionKind, jobs.EnqueueOptions{UniqueKey: "session-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookSummarizeSessionKind, "session-1", jobs.StateDone)
	})
}

func TestNotebookNarrationDisabledExecutorNoOps(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		installNotebookNarrationRunner(t, d)
		d.store.SetSetting(SettingNotebookNarrateWorkspaceEnabled, "false")

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			t.Fatal("narrate executor ran the agent while journal narration was off")
			return agentdriver.HeadlessTaskResult{}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)
	})
}

// Does not poll, unlike taskExists: the record must be absent right now.
func assertNoTask(t *testing.T, d *Daemon, kind, subject string) {
	t.Helper()
	task, err := d.jobQueue.GetByKey(kind, subject)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task != nil {
		t.Fatalf("expected no %s task for %q, got %+v", kind, subject, task)
	}
}
