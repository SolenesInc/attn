package daemon

import (
	"context"
	"fmt"
	"strings"
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
		Trigger:    rec.Trigger,
		Impact:     rec.Impact,
		Cause:      rec.Cause,
		Diagnostic: rec.Diagnostic,
		SourceKind: rec.SourceKind,
		SourceID:   rec.SourceID,
		CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, action := range rec.Actions {
		pn.Actions = append(pn.Actions, protocol.NotificationAction{
			Kind: action.Kind, Label: action.Label, TargetID: action.TargetID,
		})
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

const (
	notificationActionRetryTask   = "retry_task"
	notificationActionOpenSession = "open_session"
)

type taskFailureRenderer func(*jobs.Job) store.NotificationRecord

func (d *Daemon) registerTaskWithFailureRenderer(
	runner *jobs.Runner,
	kind string,
	handler func(context.Context, *jobs.Job) (any, error),
	config jobs.HandlerConfig,
	renderer taskFailureRenderer,
) error {
	if renderer == nil {
		return fmt.Errorf("task %s has no failure renderer", kind)
	}
	if err := runner.RegisterWith(kind, handler, config); err != nil {
		return err
	}
	if d.taskFailureRenderers == nil {
		d.taskFailureRenderers = make(map[string]taskFailureRenderer)
	}
	d.taskFailureRenderers[kind] = renderer
	return nil
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
	renderer := d.taskFailureRenderers[t.Kind]
	if renderer == nil {
		renderer = renderUnknownTaskFailure
	}
	record, err := d.store.AddNotification(renderer(t), time.Now())
	if err != nil {
		d.logf("notifications: add task-failure notification for %s: %v", t.ID, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

func taskFailureNotification(t *jobs.Job, title, trigger, impact string, actions []store.NotificationAction) store.NotificationRecord {
	detail := t.LastError
	if diagnostic := strings.TrimSpace(t.LastDiagnostic); diagnostic != "" {
		if detail != "" {
			detail += "\n\nDiagnostic output:\n"
		}
		detail += diagnostic
	}
	return store.NotificationRecord{
		Kind:       notificationKindTaskFailed,
		Severity:   store.NotificationWarning,
		Title:      title,
		Body:       impact,
		Detail:     detail,
		Trigger:    trigger,
		Impact:     impact,
		Cause:      t.LastError,
		Diagnostic: t.LastDiagnostic,
		Actions:    actions,
		SourceKind: "task",
		SourceID:   t.ID,
	}
}

func renderUnknownTaskFailure(t *jobs.Job) store.NotificationRecord {
	return taskFailureNotification(t,
		fmt.Sprintf("Background job %s failed", t.Kind),
		"A background job reached its final automatic attempt.",
		"The job did not finish. Open Background Tasks to inspect it.", nil)
}

func retryTaskAction(t *jobs.Job) store.NotificationAction {
	return store.NotificationAction{Kind: notificationActionRetryTask, Label: "Retry", TargetID: t.ID}
}

func openSessionAction(sessionID string) store.NotificationAction {
	return store.NotificationAction{Kind: notificationActionOpenSession, Label: "Open session", TargetID: sessionID}
}

func (d *Daemon) sessionFailureName(sessionID string) string {
	if d.store != nil {
		if session := d.store.Get(sessionID); session != nil && strings.TrimSpace(session.Label) != "" {
			return fmt.Sprintf("%q", session.Label)
		}
	}
	return "session " + sessionID
}

func (d *Daemon) renderSessionActivityFailure(t *jobs.Job) store.NotificationRecord {
	sessionID := jobSubject(t)
	name := d.sessionFailureName(sessionID)
	return taskFailureNotification(t,
		"Couldn’t update activity for "+name,
		"New output in "+name+" triggered a Home activity summary.",
		"Its Home activity summary may be stale. The session itself is still running.",
		[]store.NotificationAction{openSessionAction(sessionID), retryTaskAction(t)})
}

func (d *Daemon) renderSessionTitleFailure(t *jobs.Job) store.NotificationRecord {
	sessionID := jobSubject(t)
	name := d.sessionFailureName(sessionID)
	return taskFailureNotification(t,
		"Couldn’t name "+name,
		"New conversation content triggered automatic session naming.",
		"The session keeps its current fallback name.",
		[]store.NotificationAction{openSessionAction(sessionID), retryTaskAction(t)})
}

func (d *Daemon) renderSnoozeWakeFailure(t *jobs.Job) store.NotificationRecord {
	sessionID := jobSubject(t)
	name := d.sessionFailureName(sessionID)
	return taskFailureNotification(t,
		"Couldn’t wake "+name,
		"The snooze deadline passed and attn tried to wake the session.",
		"The session may remain snoozed until you wake it manually.",
		[]store.NotificationAction{openSessionAction(sessionID), retryTaskAction(t)})
}

func (d *Daemon) renderReconcileFailure(t *jobs.Job) store.NotificationRecord {
	title := strings.TrimSpace(jobSubject(t))
	if in, err := reconcileInputsFromJob(t); err == nil && strings.TrimSpace(in.Title) != "" {
		title = in.Title
	}
	if title == "" {
		title = "ticket"
	}
	return taskFailureNotification(t,
		"Couldn’t reconcile ticket "+fmt.Sprintf("%q", title),
		"A session ended before its ticket outcome was clear.",
		"The ticket may not reflect what the session completed.",
		[]store.NotificationAction{retryTaskAction(t)})
}

func (d *Daemon) renderGardenReviewFailure(t *jobs.Job) store.NotificationRecord {
	name := "a Garden review item"
	var payload gardenReviewJobPayload
	if err := t.DecodePayload(&payload); err == nil && d.store != nil {
		if item, _, found, readErr := d.readGardenReviewItem(payload.ItemID); readErr == nil && found && strings.TrimSpace(item.Title) != "" {
			name = fmt.Sprintf("%q", item.Title)
		}
	}
	return taskFailureNotification(t,
		"Couldn’t classify "+name,
		"A Garden review asked an agent to classify this item.",
		"The item remains unresolved in the Garden review.",
		[]store.NotificationAction{retryTaskAction(t)})
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
