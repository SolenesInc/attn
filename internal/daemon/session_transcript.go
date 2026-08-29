package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const sessionTranscriptPageSize = 200

func (d *Daemon) handleSessionTranscript(conn net.Conn, msg *protocol.SessionTranscriptMessage) {
	session := d.store.Get(strings.TrimSpace(msg.TargetSessionID))
	if session == nil {
		d.sendError(conn, "session_not_found")
		return
	}

	path := d.inspectableTranscriptPath(session)
	if path == "" {
		d.sendError(conn, "transcript_unavailable")
		return
	}

	page, err := transcript.ReadEventPage(path, string(session.Agent), strings.TrimSpace(protocol.Deref(msg.AfterCursor)), sessionTranscriptPageSize)
	if err != nil {
		switch {
		case errors.Is(err, transcript.ErrInvalidCursor):
			d.sendError(conn, "invalid_cursor")
		case errors.Is(err, transcript.ErrCursorMismatch):
			d.sendError(conn, "cursor_mismatch")
		case errors.Is(err, transcript.ErrCursorPastEnd):
			d.sendError(conn, "cursor_past_end")
		default:
			d.logf("session transcript read failed: session=%s err=%v", session.ID, err)
			d.sendError(conn, "transcript_unavailable")
		}
		return
	}

	events := make([]protocol.SessionTranscriptEvent, 0, len(page.Events))
	for _, event := range page.Events {
		item := protocol.SessionTranscriptEvent{
			Cursor: event.Cursor,
			Kind:   event.Kind,
		}
		if event.Timestamp != "" {
			item.Timestamp = protocol.Ptr(event.Timestamp)
		}
		if event.Role != "" {
			item.Role = protocol.Ptr(event.Role)
		}
		if event.Text != "" {
			item.Text = protocol.Ptr(event.Text)
		}
		if event.ToolName != "" {
			item.ToolName = protocol.Ptr(event.ToolName)
		}
		if event.ToolCallID != "" {
			item.ToolCallID = protocol.Ptr(event.ToolCallID)
		}
		if event.IsError {
			item.IsError = protocol.Ptr(true)
		}
		events = append(events, item)
	}

	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		SessionTranscriptResult: &protocol.SessionTranscriptResult{
			SessionID:  session.ID,
			Events:     events,
			NextCursor: page.NextCursor,
			AtEnd:      page.AtEnd,
		},
	})
}

// Exact identities only: a broad cwd/newest fallback would return a neighboring
// session's conversation.
func (d *Daemon) inspectableTranscriptPath(session *protocol.Session) string {
	if session == nil {
		return ""
	}
	binding := d.store.GetSessionConversation(session.ID)
	if binding.TranscriptPath != "" {
		if _, err := os.Stat(binding.TranscriptPath); err == nil {
			return binding.TranscriptPath
		}
	}
	if path := d.liveTranscriptPath(session.ID, session.Agent); path != "" {
		return path
	}
	path := d.findTranscriptForResume(session.Agent, binding.NativeID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	d.observeAgentConversation(agentConversationObservation{
		SessionID:      session.ID,
		NativeID:       binding.NativeID,
		TranscriptPath: path,
	})
	return path
}

func SessionTranscriptErrorMessage(code string) string {
	switch strings.TrimSpace(code) {
	case "session_not_found":
		return "The target session was not found"
	case "transcript_unavailable":
		return "The target transcript is unavailable"
	case "invalid_cursor":
		return "The transcript cursor is invalid"
	case "cursor_mismatch":
		return "The transcript cursor belongs to a different transcript"
	case "cursor_past_end":
		return "The transcript cursor is past the end of the transcript"
	default:
		return "Session transcript failed"
	}
}
