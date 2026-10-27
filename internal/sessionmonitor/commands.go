package sessionmonitor

import (
	"fmt"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/statetrace"
	"time"
)

type CommandKind uint8

const (
	Settle CommandKind = iota
	HandBack
	Snooze
	Wake
	CancelCountdown
)

type Command struct {
	Key   Key
	Kind  CommandKind
	Turn  time.Time
	Until time.Time
}

func (m *Monitor) Do(command Command, now time.Time) (View, bool) {
	if m == nil {
		return View{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keyLocked() != command.Key {
		return View{}, false
	}
	var applied bool
	switch command.Kind {
	case Settle:
		applied = m.settleLocked(command.Turn, now)
	case HandBack:
		if applied = m.settleLocked(command.Turn, now); applied {
			m.turn.AgentAt = m.turn.SettledAt
		}
	case Snooze:
		applied = m.snoozeLocked(command.Turn, command.Until, now)
	case Wake:
		if command.Until.IsZero() {
			command.Until = m.turn.SnoozedUntil
			if command.Until.IsZero() {
				return m.snapshotLocked(), true
			}
		}
		applied = m.wakeLocked(command.Until, now)
	case CancelCountdown:
		m.cancelCountdownLocked()
		applied = true
	}
	if !applied {
		return View{}, false
	}
	return m.viewLocked(), true
}

func (m *Monitor) cancelCountdownLocked() {
	removed, _ := m.stopAutoSettleLocked()
	if m.dismissal != autoSettleDismissalOff && !removed {
		m.dismissal = autoSettleDismissalOff
	} else if m.state.State == "working" {
		m.dismissal = autoSettleDismissalCovered
	} else {
		m.dismissal = autoSettleDismissalArmed
	}
}

func (m *Monitor) Key() Key {
	if m == nil {
		return Key{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyLocked()
}

func (m *Monitor) Publish(view View) bool { return m.publish(view) }

func (m *Monitor) Revision() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

func (m *Monitor) Execute(kind CommandKind, turn, until, now time.Time) (bool, error) {
	view, applied := m.Do(Command{Key: m.Key(), Kind: kind, Turn: turn, Until: until}, now)
	if kind == Snooze {
		pausepoint.At(pausepoint.SessionSnoozeCommitted)
	}
	if !applied {
		return false, nil
	}
	if !m.publish(view) {
		action := "settle"
		if kind == Snooze {
			action = "snooze"
		}
		if kind == Wake {
			action = "unsnooze"
		}
		return false, fmt.Errorf("%s is pending; attn will keep retrying until it is saved", action)
	}
	if kind == Wake {
		m.traceWake("user")
		return true, nil
	}
	m.CancelAutoSettle("settled by user")
	cause := "settle"
	if kind == HandBack {
		cause = "ask answer"
	}
	m.Trace(statetrace.Observation{Source: "user", Claim: string(view.State.V.State), Detail: view.State.V.Reason, Cause: cause, Outcome: statetrace.OutcomeApplied})
	return true, nil
}
