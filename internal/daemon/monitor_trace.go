package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) recordStateObservation(sessionID protocol.SessionID, obs statetrace.Observation) {
	if protocol.TrimID(sessionID) == "" {
		return
	}
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		monitor.Trace(obs)
		return
	}
	obs = sessionmonitor.Stamped(obs)
	d.logf("%s", obs.LogLine(sessionID))
}

func (d *Daemon) traceStateVeto(sessionID protocol.SessionID, origin sessionmonitor.Origin, claim, reason string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:     origin.Source,
		Claim:      claim,
		Detail:     origin.Detail,
		Outcome:    statetrace.OutcomeVetoed,
		Reason:     reason,
		ObservedAt: origin.ObservedAt,
	})
}

func (d *Daemon) traceStateEvidence(sessionID protocol.SessionID, origin sessionmonitor.Origin, claim string) {
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:     origin.Source,
		Claim:      claim,
		Detail:     origin.Detail,
		Outcome:    statetrace.OutcomeObserved,
		ObservedAt: origin.ObservedAt,
	})
}

func (d *Daemon) runStateExplain(ctx context.Context, c command.Caller, a command.StateExplainArgs) (any, error) {
	session := d.store.Get(protocol.SessionID(strings.TrimSpace(a.ID)))
	if session == nil || !c.Sees(session.ProfileID) {
		return nil, command.Refuse(command.CodeSessionNotFound, "no session %q; pass a full session id", a.ID)
	}
	return d.stateExplainResult(ctx, session)
}

func (d *Daemon) stateExplainResult(ctx context.Context, session *protocol.Session) (*protocol.StateExplainResult, error) {
	answer, err := Call[protocol.MonitorStateTraceResult](ctx, d.router(), protocol.MonitorStateTraceMessage{Cmd: protocol.CmdMonitorStateTrace, SessionID: session.ID})
	var recorded []statetrace.Observation
	if err == nil {
		err = json.Unmarshal([]byte(answer.Observations), &recorded)
	}
	if err != nil {
		return nil, err
	}
	observations := make([]protocol.StateExplainEntry, 0, len(recorded))
	for _, obs := range recorded {
		entry := protocol.StateExplainEntry{
			Source:     obs.Source,
			Claim:      obs.Claim,
			Outcome:    string(obs.Outcome),
			ObservedAt: obs.ObservedAt.Format(time.RFC3339Nano),
			RecordedAt: obs.RecordedAt.Format(time.RFC3339Nano),
		}
		if obs.Detail != "" {
			entry.Detail = protocol.Ptr(obs.Detail)
		}
		if obs.Cause != "" {
			entry.Cause = protocol.Ptr(obs.Cause)
		}
		if obs.Reason != "" {
			entry.Reason = protocol.Ptr(obs.Reason)
		}
		if obs.Repeats > 0 {
			entry.Repeats = protocol.Ptr(obs.Repeats)
		}
		observations = append(observations, entry)
	}
	result := &protocol.StateExplainResult{
		SessionID:    session.ID,
		Agent:        session.Agent,
		State:        string(session.State),
		Observations: observations,
		Capacity:     statetrace.DefaultCapacity,
	}
	if strings.TrimSpace(session.StateSince) != "" {
		result.StateSince = protocol.Ptr(session.StateSince)
	}
	if strings.TrimSpace(protocol.Deref(session.LastModelRequestAt)) != "" {
		result.LastModelRequestAt = protocol.Ptr(protocol.Deref(session.LastModelRequestAt))
	}
	return result, nil
}

func (d *Daemon) handleHookNotification(conn net.Conn, msg *protocol.HookNotificationMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	kind := strings.TrimSpace(msg.NotificationType)
	if kind == "" {
		d.sendError(conn, "missing notification_type")
		return
	}
	message := strings.TrimSpace(protocol.Deref(msg.Message))
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourceHookNotify, Detail: message}, kind)
	d.events().Turn(harness.FromTerminal(msg.ID), time.Now(), notificationTurn(kind, message))
	d.sendOK(conn)
}

func (d *Daemon) handleHookStopFailure(conn net.Conn, msg *protocol.HookStopFailureMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	errorType := strings.TrimSpace(msg.ErrorType)
	if errorType == "" {
		d.sendError(conn, "missing error_type")
		return
	}
	message := strings.TrimSpace(protocol.Deref(msg.ErrorMessage))
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourceHookStopFailure, Detail: message}, errorType)
	d.events().Turn(harness.FromTerminal(msg.ID), time.Now(), harness.TurnEvent{Turn: harness.TurnFailed, Detail: stopFailureDetail(errorType, message)})
	d.sendOK(conn)
}

func (d *Daemon) handleHookCompaction(conn net.Conn, msg *protocol.HookCompactionMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	phase := "finished"
	if msg.Active {
		phase = "started"
	}
	trigger := strings.TrimSpace(protocol.Deref(msg.Trigger))
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourceHookCompaction, Detail: trigger}, phase)
	d.events().Compacting(harness.FromTerminal(msg.ID), msg.Active, trigger)
	d.sendOK(conn)
}

func notificationTurn(kind, message string) harness.TurnEvent {
	switch kind {
	case "permission_prompt":
		return harness.TurnEvent{Turn: harness.TurnApproval, Detail: message}
	case "idle_prompt":
		return harness.TurnEvent{Turn: harness.TurnPromptIdle}
	}
	return harness.TurnEvent{Turn: harness.TurnUnknown}
}

func (d *Daemon) tracePermissionMode(sessionID protocol.SessionID, mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return
	}
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourcePermissionMode}, mode)
}

func stopFailureDetail(errorType, message string) string {
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail += ": " + message
	}
	return detail
}
