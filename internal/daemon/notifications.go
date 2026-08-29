package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func notificationToProtocol(rec store.NotificationRecord) protocol.Notification {
	pn := protocol.Notification{
		ID:         rec.ID,
		Kind:       rec.Kind,
		Severity:   protocol.NotificationSeverity(store.NormalizeNotificationSeverity(string(rec.Severity))),
		Title:      rec.Title,
		Body:       rec.Body,
		Detail:     rec.Detail,
		SourceKind: rec.SourceKind,
		SourceID:   rec.SourceID,
		CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !rec.ReadAt.IsZero() {
		pn.ReadAt = rec.ReadAt.UTC().Format(time.RFC3339)
	}
	return pn
}

func notificationsToProtocol(recs []store.NotificationRecord) []protocol.Notification {
	out := make([]protocol.Notification, 0, len(recs))
	for _, r := range recs {
		out = append(out, notificationToProtocol(r))
	}
	return out
}

func (d *Daemon) sendNotificationListWSResult(client *wsClient, requestID string) {
	if d.store == nil {
		d.sendToClient(client, protocol.NotificationListResultMessage{
			Event:     protocol.EventNotificationListResult,
			RequestID: requestID,
			Success:   true,
		})
		return
	}
	list, err := d.store.ListNotifications()
	unread, unreadErr := d.store.UnreadNotificationCount()
	if err == nil {
		err = unreadErr
	}
	criticalCount, criticalTitle, criticalErr := d.store.UnreadCriticalNotifications()
	if err == nil {
		err = criticalErr
	}
	msg := protocol.NotificationListResultMessage{
		Event:               protocol.EventNotificationListResult,
		RequestID:           requestID,
		Success:             err == nil,
		Notifications:       notificationsToProtocol(list),
		UnreadCount:         unread,
		UnreadCriticalCount: criticalCount,
	}
	if criticalTitle != "" {
		msg.CriticalTitle = protocol.Ptr(criticalTitle)
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendNotificationMarkReadWSResult(client *wsClient, requestID string, notificationID *string) {
	fail := func(m string) {
		d.sendToClient(client, protocol.NotificationMarkReadResultMessage{
			Event:     protocol.EventNotificationMarkReadResult,
			RequestID: requestID,
			Success:   false,
			Error:     protocol.Ptr(m),
		})
	}
	if d.store == nil {
		fail("notification store unavailable")
		return
	}
	var markedIDs []string
	var markErr error
	if notificationID != nil && *notificationID != "" {
		markedIDs = []string{*notificationID}
		markErr = d.store.MarkNotificationRead(*notificationID, time.Now())
	} else {
		if all, err := d.store.ListNotifications(); err == nil {
			for _, rec := range all {
				if rec.ReadAt.IsZero() {
					markedIDs = append(markedIDs, rec.ID)
				}
			}
		}
		_, markErr = d.store.MarkAllNotificationsRead(time.Now())
	}
	if markErr != nil {
		fail(markErr.Error())
		return
	}
	unread, err := d.store.UnreadNotificationCount()
	if err != nil {
		fail(err.Error())
		return
	}
	d.sendToClient(client, protocol.NotificationMarkReadResultMessage{
		Event:       protocol.EventNotificationMarkReadResult,
		RequestID:   requestID,
		Success:     true,
		UnreadCount: unread,
	})
	d.coalesceSnapshots(func() {
		for _, id := range markedIDs {
			d.publishFact(FactNotificationRead, id, nil)
		}
	})
}

const notificationKindTaskFailed = "task_failed"

var taskFailureTitles = map[string]string{
	compactContextKind:           "Context compaction failed",
	notebookSummarizeSessionKind: "Session summary failed",
	notebookNarrateWorkspaceKind: "Workspace narration failed",
	reconcileKind:                "Ticket reconciliation failed",
}

// Runs on the job runner's goroutine, so it must not block or panic.
func (d *Daemon) notifyTaskTerminalFailure(t *jobs.Job) {
	if t == nil || d.store == nil {
		return
	}
	if t.Kind == legacyTicketRecoveryKind {
		d.finalizeExhaustedLegacyTicketRecovery(t)
		return
	}
	record, err := d.store.AddNotification(renderTaskFailureNotification(t), time.Now())
	if err != nil {
		d.logf("notifications: add task-failure notification for %s: %v", t.ID, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

func renderTaskFailureNotification(t *jobs.Job) store.NotificationRecord {
	title := taskFailureTitles[t.Kind]
	if title == "" {
		title = fmt.Sprintf("Background task failed: %s", t.Kind)
	}
	attemptWord := "attempt"
	if t.Attempts != 1 {
		attemptWord = "attempts"
	}
	return store.NotificationRecord{
		Kind:       notificationKindTaskFailed,
		Severity:   store.NotificationWarning,
		Title:      title,
		Body:       fmt.Sprintf("attn retried %d %s and gave up. Retry to run it again.", t.Attempts, attemptWord),
		Detail:     t.LastError,
		SourceKind: "task",
		SourceID:   t.ID,
	}
}

func (d *Daemon) projectNotificationsUpdated() {
	d.projectSnapshot(snapshotNotifs, func() {
		unread, criticalCount, criticalTitle := 0, 0, ""
		if d.store != nil {
			if n, err := d.store.UnreadNotificationCount(); err == nil {
				unread = n
			}
			if n, title, err := d.store.UnreadCriticalNotifications(); err == nil {
				criticalCount, criticalTitle = n, title
			}
		}
		msg := protocol.NotificationsUpdatedMessage{
			Event:               protocol.EventNotificationsUpdated,
			UnreadCount:         unread,
			UnreadCriticalCount: criticalCount,
		}
		if criticalTitle != "" {
			msg.CriticalTitle = protocol.Ptr(criticalTitle)
		}
		d.broadcastMessage(msg)
	})
}
