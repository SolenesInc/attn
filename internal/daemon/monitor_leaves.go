package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/victorarias/attn/internal/activity"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/transcript"
)

func (d *Daemon) registerMonitorParts(parts map[string]machinePart) {
	runs(parts, false, d.runMonitorCommand)
	runs(parts, false, d.placeSessionInput)
	runs(parts, false, d.changePullRequestLink)
	runs(parts, false, d.renameLink)
	runs(parts, true, d.readTranscriptActivity)
	runs(parts, true, d.readMonitorAssistantWindow)
	runs(parts, true, d.readMonitorStateTrace)
	runs(parts, true, d.readMonitorTranscriptPage)
	runs(parts, true, d.readMonitorLastAssistant)
}

func (d *Daemon) runMonitorCommand(_ context.Context, msg protocol.MonitorCommandMessage) (protocol.MonitorCommandResult, error) {
	var result protocol.MonitorCommandResult
	monitor := d.sessionMonitor(msg.SessionID)
	if monitor == nil {
		return result, fmt.Errorf("session %s: no monitor runs it", msg.SessionID)
	}
	now := time.Now()
	if msg.Kind == "agent_turn" {
		outcome, err := monitor.AgentTurn(protocol.Deref(msg.Action), now)
		result.Applied, result.Outcome = err == nil, protocol.Ptr(outcome)
		return result, err
	}
	if msg.Kind == "cancel_countdown" {
		result.Applied, _, _ = monitor.AnswerCountdown()
		return result, nil
	}
	kinds := map[string]sessionmonitor.CommandKind{"settle": sessionmonitor.Settle, "hand_back": sessionmonitor.HandBack, "snooze": sessionmonitor.Snooze, "wake": sessionmonitor.Wake}
	kind, known := kinds[string(msg.Kind)]
	if !known {
		return result, fmt.Errorf("unknown monitor command %q", msg.Kind)
	}
	turn, err := parseMonitorTime(msg.Turn)
	if err != nil {
		return result, err
	}
	until, err := parseMonitorTime(msg.Until)
	if err != nil {
		return result, err
	}
	result.Applied, err = monitor.Execute(kind, turn, until, now)
	if err != nil {
		err = fmt.Errorf("session %s: %w", msg.SessionID, err)
	}
	return result, err
}

func parseMonitorTime(value *string) (time.Time, error) {
	if value == nil || *value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, *value)
}

func (d *Daemon) monitorCommand(session protocol.SessionID, kind string, until time.Time, action protocol.AgentTurnAction) (protocol.MonitorCommandResult, error) {
	turn := d.store.TurnStamps(session).OpenedAt.Format(time.RFC3339Nano)
	msg := protocol.MonitorCommandMessage{Cmd: protocol.CmdMonitorCommand, SessionID: session, Kind: protocol.MonitorCommandKind(kind), Turn: &turn}
	if !until.IsZero() {
		msg.Until = protocol.Ptr(until.Format(time.RFC3339Nano))
	}
	if action != "" {
		msg.Action = &action
	}
	return Call[protocol.MonitorCommandResult](context.Background(), d.router(), msg)
}

func (d *Daemon) placeSessionInput(ctx context.Context, msg protocol.SessionPlaceMessage) (protocol.SessionPlaceResult, error) {
	monitor := d.monitorInTerminal(d.primaryTerminal(msg.SessionID), msg.SessionID)
	if monitor == nil {
		return protocol.SessionPlaceResult{}, fmt.Errorf("session %s: no monitor runs it", msg.SessionID)
	}
	return monitor.Place(ctx, sessionmonitor.DeliveryFromMessage(msg)).Result(), nil
}

type placementCallError struct{ error }

func (e *placementCallError) Unwrap() error { return e.error }

func (d *Daemon) placeDelivery(ctx context.Context, session protocol.SessionID, delivery sessionmonitor.Delivery) sessionmonitor.Attempt {
	result, err := Call[protocol.SessionPlaceResult](ctx, d.router(), delivery.Message(session))
	if err != nil {
		return sessionmonitor.Attempt{Stage: sessionmonitor.Deferred, Err: &placementCallError{err}}
	}
	return sessionmonitor.AttemptFromResult(result)
}

func (d *Daemon) changePullRequestLink(_ context.Context, msg protocol.PullRequestLinkMessage) (protocol.PullRequestLinkResult, error) {
	monitor := d.sessionMonitor(msg.SessionID)
	if monitor == nil {
		return protocol.PullRequestLinkResult{}, fmt.Errorf("session %s: no monitor runs it", msg.SessionID)
	}
	if msg.Kind == "forget" {
		return protocol.PullRequestLinkResult{Applied: monitor.ForgetPullRequest(msg.PRID)}, nil
	}
	return protocol.PullRequestLinkResult{Applied: monitor.ReportPullRequest(msg.PRID, sessionmonitor.PullRequest{Repository: protocol.Deref(msg.Repository), Number: protocol.Deref(msg.Number), URL: protocol.Deref(msg.URL)})}, nil
}

func (d *Daemon) renameLink(_ context.Context, msg protocol.LinkRenameMessage) (struct{}, error) {
	if link := d.linkOf(msg.SessionID); link != nil {
		link.renamed(msg.SessionID, msg.Label)
	}
	return struct{}{}, nil
}

func (d *Daemon) readTranscriptActivity(_ context.Context, msg protocol.TranscriptActivityMessage) (protocol.TranscriptActivityResult, error) {
	var result protocol.TranscriptActivityResult
	monitor := d.sessionMonitor(msg.SessionID)
	path := monitor.ActivityTranscript()
	if path == "" {
		return result, nil
	}
	result.Available = true
	switch msg.Kind {
	case "modified":
		info, err := os.Stat(path)
		if err != nil {
			return result, err
		}
		result.ModifiedAt = protocol.Ptr(info.ModTime().Format(time.RFC3339Nano))
	case "head":
		head, err := monitor.TranscriptHead()
		if err != nil {
			return result, err
		}
		result.Head = &head
	case "events":
		window, err := monitor.TranscriptEvents(protocol.Deref(msg.Cursor))
		if err != nil {
			kind := transcriptReadErrorKind(err)
			if errors.Is(err, activity.ErrDeltaTooLarge) {
				kind = "delta_too_large"
			}
			result.ErrorKind, result.Error = protocol.Ptr(protocol.MonitorReadErrorKind(kind)), protocol.Ptr(err.Error())
			return result, nil
		}
		encoded, err := json.Marshal(window)
		if err != nil {
			return result, err
		}
		result.Window = protocol.Ptr(string(encoded))
	default:
		return result, fmt.Errorf("unknown transcript activity read %q", msg.Kind)
	}
	return result, nil
}

func (d *Daemon) readMonitorAssistantWindow(_ context.Context, msg protocol.MonitorAssistantWindowMessage) (protocol.MonitorAssistantWindowResult, error) {
	var result protocol.MonitorAssistantWindowResult
	session := d.store.Get(msg.SessionID)
	if session == nil {
		return result, fmt.Errorf("session_not_found")
	}
	window, available := d.sessionMonitor(msg.SessionID).AssistantWindow(session.Agent)
	encoded, err := json.Marshal(window)
	result.Available, result.Window = available, string(encoded)
	return result, err
}

func (d *Daemon) readMonitorStateTrace(_ context.Context, msg protocol.MonitorStateTraceMessage) (protocol.MonitorStateTraceResult, error) {
	encoded, err := json.Marshal(d.sessionMonitor(msg.SessionID).TraceSnapshot())
	return protocol.MonitorStateTraceResult{Observations: string(encoded)}, err
}

func (d *Daemon) readMonitorTranscriptPage(_ context.Context, msg protocol.MonitorTranscriptPageMessage) (protocol.MonitorTranscriptPageResult, error) {
	var result protocol.MonitorTranscriptPageResult
	session := d.monitorTranscriptSession(msg.SessionID)
	if session == nil {
		return result, fmt.Errorf("session_not_found")
	}
	path := d.inspectableTranscriptPath(session.ID, session.Agent)
	if path == "" {
		result.ErrorKind = protocol.Ptr(protocol.MonitorReadErrorKindTranscriptUnavailable)
		return result, nil
	}
	page, err := transcript.ReadEventPage(path, session.Agent, protocol.Deref(msg.Cursor), sessionTranscriptPageSize)
	if err != nil {
		result.ErrorKind = protocol.Ptr(protocol.MonitorReadErrorKind(transcriptReadErrorKind(err)))
		return result, nil
	}
	encoded, err := json.Marshal(page)
	result.Page = protocol.Ptr(string(encoded))
	return result, err
}

func transcriptReadErrorKind(err error) string {
	switch {
	case errors.Is(err, transcript.ErrInvalidCursor):
		return "invalid_cursor"
	case errors.Is(err, transcript.ErrCursorMismatch):
		return "cursor_mismatch"
	case errors.Is(err, transcript.ErrCursorPastEnd):
		return "cursor_past_end"
	default:
		return "transcript_unavailable"
	}
}

func (d *Daemon) readMonitorLastAssistant(_ context.Context, msg protocol.MonitorLastAssistantMessage) (protocol.MonitorLastAssistantResult, error) {
	session := d.monitorTranscriptSession(msg.SessionID)
	if session == nil {
		return protocol.MonitorLastAssistantResult{}, fmt.Errorf("session_not_found")
	}
	path := d.inspectableTranscriptPath(session.ID, session.Agent)
	if path == "" {
		return protocol.MonitorLastAssistantResult{}, nil
	}
	message, err := transcript.ExtractLastAssistantMessage(path, agentPeekMessageMaxChars)
	return protocol.MonitorLastAssistantResult{Text: message}, err
}

func (d *Daemon) monitorTranscriptSession(id protocol.SessionID) *protocol.SessionLedgerEntry {
	if session := d.store.SessionLedgerEntry(id); session != nil {
		return session
	}
	records := d.store.SessionRuntimeRecords(id)
	if records.HomeDaemonID == "" || records.Agent == "" {
		return nil
	}
	return &protocol.SessionLedgerEntry{ID: id, Agent: records.Agent}
}
