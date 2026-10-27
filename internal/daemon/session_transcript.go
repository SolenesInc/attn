package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/transcript"
)

const sessionTranscriptPageSize = 200

func (d *Daemon) handleSessionTranscript(conn net.Conn, msg *protocol.SessionTranscriptMessage) {
	session := d.store.SessionLedgerEntry(protocol.SessionID(strings.TrimSpace(msg.TargetSessionID)))
	if session == nil {
		d.sendError(conn, "session_not_found")
		return
	}

	answer, err := Call[protocol.MonitorTranscriptPageResult](context.Background(), d.router(), protocol.MonitorTranscriptPageMessage{Cmd: protocol.CmdMonitorTranscriptPage, SessionID: session.ID, Cursor: protocol.Ptr(strings.TrimSpace(protocol.Deref(msg.AfterCursor)))})
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if answer.ErrorKind != nil {
		d.sendError(conn, string(*answer.ErrorKind))
		return
	}
	var page transcript.EventPage
	if err := json.Unmarshal([]byte(protocol.Deref(answer.Page)), &page); err != nil {
		d.sendError(conn, "transcript_unavailable")
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

func (d *Daemon) inspectableTranscriptPath(sessionID protocol.SessionID, agent string) string {
	binding := d.store.GetSessionConversation(sessionID)
	if records := d.store.SessionRuntimeRecords(sessionID); records.HomeDaemonID != "" && binding.NativeID == "" {
		binding.NativeID, binding.TranscriptPath = records.ConversationNativeID, records.ConversationTranscriptPath
	}
	for _, path := range []string{binding.TranscriptPath, d.store.GetAgentDriverTranscriptPath(sessionID)} {
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	if path := d.sessionMonitor(sessionID).LiveTranscriptPath(agent); path != "" {
		return path
	}
	driver := agentdriver.Get(agent)
	if _, keeper := driver.(agentdriver.ConversationKeeper); keeper && !d.conversationReady(driver, binding.NativeID) {
		return ""
	}
	if binding.TranscriptPath != "" {
		if _, err := os.Stat(binding.TranscriptPath); err == nil {
			return binding.TranscriptPath
		}
	}
	path := sessionmonitor.FindTranscriptForResume(agent, binding.NativeID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
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
