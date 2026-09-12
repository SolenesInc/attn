package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestNotifyTaskTerminalFailurePersistsNotification(t *testing.T) {
	d := &Daemon{store: store.New()} // nil wsHub: broadcast is a guarded no-op
	d.taskFailureRenderers = map[string]taskFailureRenderer{reconcileKind: d.renderReconcileFailure}

	d.notifyTaskTerminalFailure(&jobs.Job{
		ID:             "job-1",
		Kind:           reconcileKind,
		UniqueKey:      "t-1",
		State:          jobs.StateDead,
		Attempts:       3,
		LastError:      "boom: context deadline exceeded",
		LastDiagnostic: "stderr: authentication failed",
	})

	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(list))
	}
	n := list[0]
	if n.Kind != notificationKindTaskFailed {
		t.Fatalf("kind = %q, want %q", n.Kind, notificationKindTaskFailed)
	}
	if n.Title != "Couldn’t reconcile ticket \"t-1\"" {
		t.Fatalf("title = %q", n.Title)
	}
	if n.Trigger == "" || n.Impact == "" || n.Cause != "boom: context deadline exceeded" ||
		n.Diagnostic != "stderr: authentication failed" {
		t.Fatalf("structured failure = %+v", n)
	}
	if len(n.Actions) != 1 || n.Actions[0].Kind != notificationActionRetryTask || n.Actions[0].TargetID != "job-1" {
		t.Fatalf("actions = %+v", n.Actions)
	}
	if n.SourceKind != "task" || n.SourceID != "job-1" {
		t.Fatalf("source = %s/%s, want task/job-1", n.SourceKind, n.SourceID)
	}
	if !n.ReadAt.IsZero() {
		t.Fatalf("expected unread notification")
	}
	if unread, _ := d.store.UnreadNotificationCount(); unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}
}

func TestNotifyTaskTerminalFailureNilStoreIsNoop(t *testing.T) {
	d := &Daemon{}
	d.notifyTaskTerminalFailure(&jobs.Job{Kind: reconcileKind, State: jobs.StateDead})
}

func TestRenderTaskFailureNotification(t *testing.T) {
	d := &Daemon{store: store.New()}
	got := d.renderReconcileFailure(&jobs.Job{
		ID: "job-9", Kind: reconcileKind, UniqueKey: "t-9", Attempts: 1, LastError: "nope",
	})
	if got.Title != "Couldn’t reconcile ticket \"t-9\"" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.Trigger == "" || got.Impact == "" || got.Cause != "nope" {
		t.Fatalf("structured notification = %+v", got)
	}
	other := renderUnknownTaskFailure(&jobs.Job{ID: "job-x", Kind: "mystery", Attempts: 2})
	if other.Title != "Background job mystery failed" {
		t.Fatalf("unknown-kind title = %q", other.Title)
	}
	if len(other.Actions) != 0 {
		t.Fatalf("unknown kind offered actions: %+v", other.Actions)
	}
}

func TestTaskFailureRenderersDescribeEachTrigger(t *testing.T) {
	d := &Daemon{}
	task := &jobs.Job{ID: "job-1", UniqueKey: "session-1", LastError: "safe cause"}

	tests := []struct {
		name        string
		render      taskFailureRenderer
		wantActions []string
	}{
		{"session activity", d.renderSessionActivityFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"session title", d.renderSessionTitleFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"snooze wake", d.renderSnoozeWakeFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"reconcile", d.renderReconcileFailure, []string{notificationActionRetryTask}},
		{"Garden review", d.renderGardenReviewFailure, []string{notificationActionRetryTask}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.render(task)
			if got.Title == "" || got.Trigger == "" || got.Impact == "" || got.Cause != "safe cause" {
				t.Fatalf("notification = %+v", got)
			}
			if len(got.Actions) != len(test.wantActions) {
				t.Fatalf("actions = %+v, want %v", got.Actions, test.wantActions)
			}
			for i, want := range test.wantActions {
				if got.Actions[i].Kind != want {
					t.Fatalf("action %d = %q, want %q", i, got.Actions[i].Kind, want)
				}
			}
		})
	}
}

func TestRegisterTaskWithFailureRendererRequiresRenderer(t *testing.T) {
	d := &Daemon{}
	if err := d.registerTaskWithFailureRenderer(nil, "kind", nil, jobs.HandlerConfig{}, nil); err == nil {
		t.Fatal("register without failure renderer succeeded")
	}
}

func TestTaskFailureNotificationIsWarning(t *testing.T) {
	d := &Daemon{store: store.New()}
	if got := d.renderReconcileFailure(&jobs.Job{
		ID: "job-9", Kind: reconcileKind, Attempts: 1,
	}).Severity; got != store.NotificationWarning {
		t.Fatalf("severity = %q, want warning", got)
	}

	d.taskFailureRenderers = map[string]taskFailureRenderer{reconcileKind: d.renderReconcileFailure}
	d.notifyTaskTerminalFailure(&jobs.Job{
		ID: "job-1", Kind: reconcileKind, State: jobs.StateDead, Attempts: 3,
	})
	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Severity != store.NotificationWarning {
		t.Fatalf("persisted severity = %+v, want one warning row", list)
	}

	n, title, err := d.store.UnreadCriticalNotifications()
	if err != nil {
		t.Fatalf("unread critical: %v", err)
	}
	if n != 0 || title != "" {
		t.Fatalf("a dead job lit the critical surface: (%d, %q)", n, title)
	}
}

func TestNotificationToProtocolCarriesSeverity(t *testing.T) {
	for _, tc := range []struct {
		stored store.NotificationSeverity
		want   protocol.NotificationSeverity
	}{
		{store.NotificationInfo, protocol.NotificationSeverityInfo},
		{store.NotificationWarning, protocol.NotificationSeverityWarning},
		{store.NotificationCritical, protocol.NotificationSeverityCritical},
		{"", protocol.NotificationSeverityInfo},
		{"nonsense", protocol.NotificationSeverityInfo},
	} {
		got := notificationToProtocol(store.NotificationRecord{Severity: tc.stored})
		if got.Severity != tc.want {
			t.Fatalf("stored %q → wire %q, want %q", tc.stored, got.Severity, tc.want)
		}
	}
}
