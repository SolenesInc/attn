package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) recordStateObservation(sessionID protocol.SessionID, obs statetrace.Observation) {
	if protocol.TrimID(sessionID) == "" {
		return
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		entry.Trace(obs)
		return
	}
	obs = sessionmonitor.Stamped(obs)
	d.logf("%s", obs.LogLine(sessionID))
	if sessionmonitor.HarnessReportedState(obs.Source) {
		d.noteLaunchStarted(sessionID)
	}
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

func (d *Daemon) handleStateExplain(conn net.Conn, msg *protocol.StateExplainMessage) {
	session := d.store.Get(protocol.SessionID(strings.TrimSpace(msg.TargetSessionID)))
	if session == nil {
		d.sendError(conn, "session_not_found")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                 true,
		StateExplainResult: d.stateExplainResult(session),
	})
}

func (d *Daemon) stateExplainResult(session *protocol.Session) *protocol.StateExplainResult {
	recorded := d.monitorEntry(session.ID).TraceSnapshot()
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
	return result
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
	d.monitorEntry(sessionID).RecordNotification(kind, message)
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
	d.monitorEntry(sessionID).RecordStopFailure(errorType, message)
	d.sendOK(conn)
}

func (d *Daemon) handleHookCompaction(conn net.Conn, msg *protocol.HookCompactionMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	phase := "finished"
	if msg.Active {
		phase = "started"
	}
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourceHookCompaction, Detail: strings.TrimSpace(protocol.Deref(msg.Trigger))}, phase)
	d.monitorEntry(sessionID).RecordCompaction(msg.Active)
	d.sendOK(conn)
}

func (d *Daemon) tracePermissionMode(sessionID protocol.SessionID, mode string) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return
	}
	d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: sessionmonitor.StateSourcePermissionMode}, mode)
}
