package sessionmonitor

import (
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/statetrace"
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

func (k CommandKind) action() string {
	switch k {
	case Snooze:
		return "snooze"
	case Wake:
		return "unsnooze"
	default:
		return "settle"
	}
}

func (m *Monitor) Do(command Command, now time.Time) (View, error) {
	if m == nil {
		return View{}, errors.New("no monitor runs the session")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keyLocked() != command.Key {
		return View{}, errors.New("the session moved to another terminal or conversation")
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
				return m.snapshotLocked(), nil
			}
		}
		applied = m.wakeLocked(command.Until, now)
	case CancelCountdown:
		m.cancelCountdownLocked()
		applied = true
	}
	if !applied {
		return View{}, m.refusalLocked(command)
	}
	return m.viewLocked(), nil
}

func (m *Monitor) refusalLocked(command Command) error {
	if command.Kind == Wake {
		return fmt.Errorf("the snooze until %s already ended", command.Until.Format(time.RFC3339Nano))
	}
	return fmt.Errorf("the turn changed: the current turn opened at %s; %s again to apply it to that turn", m.turn.OpenedAt.Format(time.RFC3339Nano), command.Kind.action())
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
	if kind == Snooze {
		pausepoint.At(pausepoint.SessionSnoozeArrived)
	}
	view, err := m.Do(Command{Key: m.Key(), Kind: kind, Turn: turn, Until: until}, now)
	if kind == Snooze {
		pausepoint.At(pausepoint.SessionSnoozeDecided)
	}
	if err != nil {
		return false, fmt.Errorf("%s was not saved because %w", kind.action(), err)
	}
	if !m.publish(view) {
		return false, fmt.Errorf("%s is pending; attn will keep retrying until it is saved", kind.action())
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
