package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Tripwires, not budgets. Measured over 120 recent Claude Code transcripts (2,327 prose
// blocks): largest block 18,713 chars (p99 3,949); the last 32 totalled at most 21,282.
const (
	annotatableMessageMaxChars = 64 * 1024
	annotatableWindowMessages  = 32
	annotatableWindowMaxChars  = 256 * 1024
)

// A transcript with no annotatable prose comes back as an empty list with success=true, not an error.
func (d *Daemon) handleSessionMessagesGet(client *wsClient, msg *protocol.SessionMessagesGetMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	result := protocol.SessionMessagesGetResultMessage{
		Event:     protocol.EventSessionMessagesGetResult,
		RequestID: msg.RequestID,
		SessionID: sessionID,
		Messages:  []protocol.SessionMessage{},
		Status:    protocol.SessionMessageWindowStatusUnavailable,
	}
	if sessionID == "" {
		result.Error = protocol.Ptr("session_messages_get: session_id is required")
		d.sendToClient(client, result)
		return
	}

	session := d.store.Get(sessionID)
	if session == nil {
		result.Error = protocol.Ptr("session_messages_get: unknown session " + sessionID)
		d.sendToClient(client, result)
		return
	}

	snapshot, ok := d.assistantWindow(sessionID, session.Agent)
	if !ok {
		detail := "live transcript watching is unavailable for this session"
		result.Success = true
		result.Detail = protocol.Ptr(detail)
		d.logf("session_messages_get: %s: %s (agent=%s dir=%s)", sessionID, detail, session.Agent, session.Directory)
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Status = snapshot.Status
	if snapshot.Detail != "" {
		result.Detail = protocol.Ptr(snapshot.Detail)
	}
	if snapshot.Report.DroppedOversize > 0 {
		d.logf("session_messages_get: %s: dropped %d message(s) over the %d-char per-message cap (largest %d chars); annotations cannot address a partial message",
			sessionID, snapshot.Report.DroppedOversize, annotatableMessageMaxChars, snapshot.Report.LargestDropped)
	}
	if snapshot.Report.DroppedOld > 0 {
		d.logf("session_messages_get: %s: window held %d of %d message(s); caps are %d messages / %d chars",
			sessionID, len(snapshot.Messages), len(snapshot.Messages)+snapshot.Report.DroppedOld, annotatableWindowMessages, annotatableWindowMaxChars)
	}
	if snapshot.Report.OmittedPrefix {
		d.logf("session_messages_get: %s: annotatable window began at the bounded transcript tail", sessionID)
	}

	for _, message := range snapshot.Messages {
		result.Messages = append(result.Messages, protocol.SessionMessage{
			Key:      message.Key,
			Markdown: message.Content,
		})
	}
	result.Truncated = snapshot.Report.Truncated()
	d.sendToClient(client, result)
}
