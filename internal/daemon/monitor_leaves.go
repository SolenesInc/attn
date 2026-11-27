package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

func (d *Daemon) registerMonitorParts(parts map[string]machinePart) {
	runs(parts, false, d.runMonitorCommand)
	runs(parts, false, d.placeSessionInput)
	runs(parts, false, d.changePullRequestLink)
	runs(parts, false, d.saveSessionPullRequest)
	runs(parts, false, d.settleInboxRing)
	runs(parts, true, d.readClaudeModReach)
	runs(parts, false, d.receiveClaudeModReachReport)
	runs(parts, false, d.beginCompactingClose)
	runs(parts, true, d.checkCompactingClose)
	runs(parts, false, d.finishCompactingClose)
	runs(parts, false, d.receiveSessionInputReopened)
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
	if msg.Kind == "surface" {
		opened, err := monitor.Surface(now)
		if err != nil {
			return result, fmt.Errorf("session %s: %w", msg.SessionID, err)
		}
		result.Applied, result.OpenedAt = true, protocol.Ptr(opened.Format(time.RFC3339Nano))
		return result, nil
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
	result := monitor.Place(ctx, sessionmonitor.DeliveryFromMessage(msg)).Result()
	if msg.Ring != nil {
		pausepoint.At(pausepoint.RingPlacementAnswering)
	}
	return result, nil
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

func (d *Daemon) changePullRequestLink(ctx context.Context, msg protocol.PullRequestLinkMessage) (protocol.PullRequestLinkResult, error) {
	monitor := d.sessionMonitor(msg.SessionID)
	if monitor == nil {
		return protocol.PullRequestLinkResult{}, fmt.Errorf("session %s: no monitor runs it", msg.SessionID)
	}
	if msg.Kind == "forget" {
		return protocol.PullRequestLinkResult{Applied: monitor.ForgetPullRequest(msg.PRID)}, nil
	}
	link := sessionmonitor.PullRequest{Repository: protocol.Deref(msg.Repository), Number: protocol.Deref(msg.Number), URL: protocol.Deref(msg.URL)}
	if monitor.ReportPullRequest(msg.PRID, link) {
		return protocol.PullRequestLinkResult{Applied: true}, nil
	}
	if d.sessionMonitor(msg.SessionID) == monitor {
		return protocol.PullRequestLinkResult{}, nil
	}
	save := protocol.SessionPullRequestSaveMessage{Cmd: protocol.CmdSessionPullRequestSave, SessionID: msg.SessionID, PRID: msg.PRID, Repository: link.Repository, Number: link.Number, URL: link.URL}
	if _, err := Call[struct{}](ctx, d.router(), save); err != nil {
		return protocol.PullRequestLinkResult{}, err
	}
	return protocol.PullRequestLinkResult{Applied: true}, nil
}

func (d *Daemon) saveSessionPullRequest(ctx context.Context, msg protocol.SessionPullRequestSaveMessage) (struct{}, error) {
	if origin, remote := OriginOf(ctx); remote && d.sessionHost(msg.SessionID) != origin.Host {
		return struct{}{}, fmt.Errorf("session %s does not run on %s", msg.SessionID, origin.Name)
	}
	recorded, err := d.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
		SessionID: msg.SessionID, PRID: msg.PRID, Repository: msg.Repository, Number: msg.Number, URL: msg.URL,
	}, time.Now())
	if err != nil {
		return struct{}{}, fmt.Errorf("pull request %s: %w", msg.PRID, err)
	}
	if recorded {
		d.publishFact(FactSessionPullRequestChanged, string(msg.SessionID), sessionPullRequestFact{PRID: msg.PRID})
	}
	return struct{}{}, nil
}

func (d *Daemon) renameLink(_ context.Context, msg protocol.LinkRenameMessage) (struct{}, error) {
	if renamer, ok := harness.As[harness.Renamer](d.harnessOf(d.agentOf(msg.SessionID))); ok {
		renamer.Rename(msg.SessionID, msg.Label)
	}
	return struct{}{}, nil
}

func (d *Daemon) readTranscriptActivity(ctx context.Context, msg protocol.TranscriptActivityMessage) (protocol.TranscriptActivityResult, error) {
	ref := harness.ConversationRef{Session: msg.SessionID}
	if session := d.monitorTranscriptSession(msg.SessionID); session != nil {
		ref = d.ledgerConversation(session)
	}
	return d.harnessOf(ref.Harness).Conversations().Activity(ctx, ref, string(msg.Kind), protocol.Deref(msg.Cursor))
}

func (d *Daemon) readMonitorAssistantWindow(ctx context.Context, msg protocol.MonitorAssistantWindowMessage) (protocol.MonitorAssistantWindowResult, error) {
	var result protocol.MonitorAssistantWindowResult
	session := d.store.Get(msg.SessionID)
	if session == nil {
		return result, fmt.Errorf("session_not_found")
	}
	window, available := d.assistantWindow(ctx, session)
	encoded, err := json.Marshal(window)
	result.Available, result.Window = available, string(encoded)
	return result, err
}

func (d *Daemon) readMonitorStateTrace(_ context.Context, msg protocol.MonitorStateTraceMessage) (protocol.MonitorStateTraceResult, error) {
	encoded, err := json.Marshal(d.sessionMonitor(msg.SessionID).TraceSnapshot())
	return protocol.MonitorStateTraceResult{Observations: string(encoded)}, err
}

func (d *Daemon) readMonitorTranscriptPage(ctx context.Context, msg protocol.MonitorTranscriptPageMessage) (protocol.MonitorTranscriptPageResult, error) {
	var result protocol.MonitorTranscriptPageResult
	session := d.monitorTranscriptSession(msg.SessionID)
	if session == nil {
		return result, fmt.Errorf("session_not_found")
	}
	page, err := d.transcriptPage(ctx, session, protocol.Deref(msg.Cursor))
	if err != nil {
		result.ErrorKind = protocol.Ptr(protocol.MonitorReadErrorKind(transcript.ReadErrorKind(err)))
		return result, nil
	}
	encoded, err := json.Marshal(page)
	result.Page = protocol.Ptr(string(encoded))
	return result, err
}

func (d *Daemon) readMonitorLastAssistant(ctx context.Context, msg protocol.MonitorLastAssistantMessage) (protocol.MonitorLastAssistantResult, error) {
	session := d.monitorTranscriptSession(msg.SessionID)
	if session == nil {
		return protocol.MonitorLastAssistantResult{}, fmt.Errorf("session_not_found")
	}
	ref := d.ledgerConversation(session)
	message, err := d.harnessOf(ref.Harness).Conversations().LastReply(ctx, ref, agentPeekMessageMaxChars)
	return protocol.MonitorLastAssistantResult{Text: message}, err
}

func (d *Daemon) transcriptPage(ctx context.Context, session *protocol.SessionLedgerEntry, cursor string) (transcript.EventPage, error) {
	ref := d.ledgerConversation(session)
	return d.harnessOf(ref.Harness).Conversations().Page(ctx, ref, cursor, sessionTranscriptPageSize)
}

func (d *Daemon) ledgerConversation(session *protocol.SessionLedgerEntry) harness.ConversationRef {
	return harness.ConversationRef{Harness: session.Agent, Session: session.ID, ID: d.store.GetSessionConversation(session.ID).NativeID}
}

func (d *Daemon) monitorTranscriptSession(id protocol.SessionID) *protocol.SessionLedgerEntry {
	if session := d.store.SessionLedgerEntry(id); session != nil {
		return session
	}
	records := d.store.SessionRuntimeRecords(id)
	if records.HomeID == "" || records.Agent == "" {
		return nil
	}
	return &protocol.SessionLedgerEntry{ID: id, Agent: records.Agent}
}

func (d *Daemon) assistantWindow(ctx context.Context, session *protocol.Session) (sessionmonitor.AssistantWindow, bool) {
	ref := harness.ConversationRef{Harness: session.Agent, Session: session.ID, ID: d.store.GetSessionConversation(session.ID).NativeID}
	return d.harnessOf(ref.Harness).Conversations().Window(ctx, ref)
}
