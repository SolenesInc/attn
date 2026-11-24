package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestNotificationsListNewestFirstAndTrackWhatTheUserRead(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		installCrashingPlugin(t, w, "alpha")
		w.restart()
		app := w.App()
		w.advance(3 * time.Minute)
		testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool {
			return m.UnreadCount == 1 && m.UnreadCriticalCount == 1 && protocol.Deref(m.CriticalTitle) == "Plugin stopped: alpha"
		})

		if err := os.RemoveAll(filepath.Join(w.Dir, "plugins", "alpha")); err != nil {
			t.Fatal(err)
		}
		installCrashingPlugin(t, w, "beta")
		w.restart()
		app = w.App()
		w.advance(3 * time.Minute)
		testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool {
			return m.UnreadCount == 2 && m.UnreadCriticalCount == 2 && protocol.Deref(m.CriticalTitle) == "Plugin stopped: beta"
		})

		feed := listNotifications(app)
		if len(feed.Notifications) != 2 || feed.UnreadCount != 2 || feed.UnreadCriticalCount != 2 {
			t.Fatalf("feed = %+v, want both stopped plugins unread", feed)
		}
		beta, alpha := feed.Notifications[0], feed.Notifications[1]
		if beta.Title != "Plugin stopped: beta" || alpha.Title != "Plugin stopped: alpha" {
			t.Fatalf("feed lists %q then %q, want the newest first", beta.Title, alpha.Title)
		}
		if alpha.Kind != "plugin_parked" || alpha.Severity != protocol.NotificationSeverityCritical || alpha.SourceKind != "plugin" ||
			alpha.SourceID != "alpha" || !strings.Contains(alpha.Detail, "exit status 3") || alpha.Body == "" || alpha.CreatedAt == "" || alpha.ReadAt != "" {
			t.Errorf("the alpha notification = %+v, want an unread critical plugin_parked record naming the exit", alpha)
		}

		if read := markNotificationRead(app, protocol.Ptr(beta.ID)); !read.Success || read.UnreadCount != 1 {
			t.Fatalf("marking beta read = %+v, want one unread left", read)
		}
		testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool {
			return m.UnreadCount == 1 && m.UnreadCriticalCount == 1 && protocol.Deref(m.CriticalTitle) == "Plugin stopped: alpha"
		})
		firstRead := listNotifications(app).Notifications[0].ReadAt
		w.advance(time.Minute)
		if again := markNotificationRead(app, protocol.Ptr(beta.ID)); !again.Success || again.UnreadCount != 1 {
			t.Errorf("marking beta read again = %+v, want it harmless", again)
		}
		if unknown := markNotificationRead(app, protocol.Ptr("no-such-notification")); !unknown.Success || unknown.UnreadCount != 1 {
			t.Errorf("marking an unknown notification read = %+v, want it harmless", unknown)
		}
		if readAt := listNotifications(app).Notifications[0].ReadAt; firstRead == "" || readAt != firstRead {
			t.Errorf("beta read at %q, then %q after marking it again; want the first read to stand", firstRead, readAt)
		}

		for range 2 {
			if all := markNotificationRead(app, nil); !all.Success || all.UnreadCount != 0 {
				t.Errorf("marking everything read = %+v, want nothing unread", all)
			}
		}
		if cleared := listNotifications(app); cleared.UnreadCount != 0 || cleared.UnreadCriticalCount != 0 || cleared.CriticalTitle != nil {
			t.Errorf("feed after marking everything read = %+v, want no unread or critical alert", cleared)
		}
	})
}

func installCrashingPlugin(t *testing.T, w *world, name string) {
	t.Helper()
	dir := filepath.Join(w.Dir, "plugins", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"0.1.0\"\nattn_api_version = 7\n\n[plugin]\nkind = \"executable\"\npath = \"run\"\n"
	if err := os.WriteFile(filepath.Join(dir, "attn-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func listNotifications(app *testworld.Peer) protocol.NotificationListResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	listed := testworld.Request(app, protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr(requestID)},
		protocol.EventNotificationListResult, func(r protocol.NotificationListResultMessage) bool { return r.RequestID == requestID })
	if !listed.Success {
		app.T.Fatalf("list notifications refused: %s", protocol.Deref(listed.Error))
	}
	return listed
}

func markNotificationRead(app *testworld.Peer, id *string) protocol.NotificationMarkReadResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.NotificationMarkReadMessage{Cmd: protocol.CmdNotificationMarkRead, NotificationID: id, RequestID: protocol.Ptr(requestID)},
		protocol.EventNotificationMarkReadResult, func(r protocol.NotificationMarkReadResultMessage) bool { return r.RequestID == requestID })
}

func TestATaskThatFailsForGoodLeavesOneWarningWhoseRetryRequeuesIt(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			task.Fail("API Error: 529 overloaded")
		})
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("the login form rejects valid passwords")
		})
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)

		feed := listNotifications(app)
		if len(feed.Notifications) != 1 || feed.UnreadCount != 1 || feed.UnreadCriticalCount != 0 {
			t.Fatalf("feed = %+v, want one unread warning and nothing critical", feed)
		}
		warning := feed.Notifications[0]
		if warning.Kind != "task_failed" || warning.Severity != protocol.NotificationSeverityWarning || warning.SourceKind != "task" ||
			!strings.Contains(warning.Title, "shop") || warning.Trigger == "" || warning.Impact == "" || warning.Cause == "" {
			t.Errorf("the warning = %+v, want a task_failed warning naming the session, its trigger, impact and cause", warning)
		}
		if len(warning.Actions) != 2 || warning.Actions[0].Kind != "open_session" || warning.Actions[0].TargetID != session ||
			warning.Actions[1].Kind != "retry_task" || warning.Actions[1].TargetID != warning.SourceID {
			t.Fatalf("the warning offers %+v, want open_session and retry_task", warning.Actions)
		}

		requestID := uuid.NewString()
		retried := testworld.Request(app, protocol.TaskRetryMessage{Cmd: protocol.CmdTaskRetry, TaskID: warning.Actions[1].TargetID, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskRetryResult, func(r protocol.TaskRetryResultMessage) bool { return r.RequestID == requestID })
		if !retried.Success || retried.Task == nil || retried.Task.State != "queued" || retried.Task.Attempts != 0 {
			t.Fatalf("retrying the dead task = %+v, want it queued with its attempts reset", retried)
		}
		w.advance(0)
		if again := listNotifications(app); len(again.Notifications) != 1 {
			t.Errorf("after the retried task failed once more the feed holds %d notifications, want still one", len(again.Notifications))
		}
	})
}

func TestATaskFailureWarnsOnlyTheSessionsProfile(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			if strings.HasPrefix(task.Prompt, "Classify how this assistant message ends") {
				task.Answer(`{"verdict":"DONE"}`)
				return
			}
			task.Fail("API Error: 529 overloaded")
		})
		defaultApp := w.App()
		side := createProfile(defaultApp, "Side")
		sideApp := w.AppOn(side.ID)
		w.Spawn(sideApp, fakeagent.Claude, w.Path("side-shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("the login form rejects valid passwords")
		})
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)

		sideFeed := listNotifications(sideApp)
		if len(sideFeed.Notifications) != 1 || sideFeed.UnreadCount != 1 || sideFeed.Notifications[0].Kind != "task_failed" ||
			!strings.Contains(sideFeed.Notifications[0].Title, "side-shop") {
			t.Fatalf("Side's feed = %+v, want one unread warning naming its session", sideFeed)
		}
		if defaultFeed := listNotifications(defaultApp); len(defaultFeed.Notifications) != 0 || defaultFeed.UnreadCount != 0 {
			t.Fatalf("a failure in Side's session reached Default: %+v", defaultFeed)
		}

		warning := sideFeed.Notifications[0]
		if read := markNotificationRead(defaultApp, protocol.Ptr(warning.ID)); !read.Success || read.UnreadCount != 0 {
			t.Errorf("Default marking Side's warning read = %+v, want it harmless to Default", read)
		}
		if after := listNotifications(sideApp); after.UnreadCount != 1 || after.Notifications[0].ReadAt != "" {
			t.Fatalf("Default marked Side's warning read: %+v", after)
		}
		if read := markNotificationRead(sideApp, protocol.Ptr(warning.ID)); !read.Success || read.UnreadCount != 0 {
			t.Errorf("Side marking its own warning read = %+v, want nothing unread", read)
		}
	})
}
