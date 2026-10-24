package sessionmonitor

import "time"

type CommandKind uint8

const (
	Settle CommandKind = iota
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
