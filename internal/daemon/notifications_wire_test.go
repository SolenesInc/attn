package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
	manifest := "name = \"" + name + "\"\nversion = \"0.1.0\"\nattn_api_version = 6\n\n[plugin]\nkind = \"executable\"\npath = \"run\"\n"
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
