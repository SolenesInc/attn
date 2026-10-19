package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

const (
	StateSourceHook            = "hook_state"
	StateSourceStopHook        = "hook_stop"
	StateSourceClassifier      = "classifier"
	StateSourceTranscript      = "transcript_watcher"
	StateSourcePluginDriver    = "plugin_driver"
	StateSourceLink            = "link"
	StateSourceHookNotify      = "hook_notify"
	StateSourceHookStopFailure = "hook_stop_failure"
	StateSourceHookCompaction  = "hook_compaction"
	StateSourcePermissionMode  = "permission_mode"
	StateSourceResolver        = "resolver"
)

func HarnessReportedState(source string) bool {
	switch source {
	case StateSourceHook, StateSourceStopHook, StateSourceHookNotify, StateSourceHookStopFailure,
		StateSourceHookCompaction, StateSourceTranscript, StateSourcePluginDriver, StateSourceLink:
		return true
	}
	return false
}

func Stamped(obs statetrace.Observation) statetrace.Observation {
	if obs.RecordedAt.IsZero() {
		obs.RecordedAt = time.Now()
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = obs.RecordedAt
	}
	return obs
}

func (e *Entry) Trace(obs statetrace.Observation) {
	if e == nil {
		return
	}
	obs = Stamped(obs)
	var report *View
	e.mu.Lock()
	if e.trace == nil {
		e.trace = statetrace.NewRing(statetrace.DefaultCapacity)
	}
	e.trace.Record(obs)
	if e.firstReportAt.IsZero() && HarnessReportedState(obs.Source) {
		e.firstReportAt = obs.RecordedAt
		view := e.viewLocked()
		report = &view
	}
	e.mu.Unlock()
	e.logf("%s", obs.LogLine(protocol.SessionID(e.Label())))
	if report != nil {
		e.home.Applied(*report)
	}
}

func (e *Entry) TraceSnapshot() []statetrace.Observation {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.trace.Snapshot()
}

func ChangeObservation(change Change, outcome statetrace.Outcome, reason string) statetrace.Observation {
	source := change.Origin.Source
	if source == "" {
		source = CauseName(change.Cause)
	}
	return statetrace.Observation{
		Source:     source,
		Claim:      change.State,
		Detail:     change.Origin.Detail,
		Cause:      CauseName(change.Cause),
		Outcome:    outcome,
		Reason:     reason,
		ObservedAt: change.Origin.ObservedAt,
	}
}

func (e *Entry) TraceChange(change Change, outcome statetrace.Outcome, reason string) {
	if e == nil {
		return
	}
	e.Trace(ChangeObservation(change, outcome, reason))
}
