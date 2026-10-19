package daemon

import "time"

type monitorCommandKind uint8

const (
	monitorSettle monitorCommandKind = iota
	monitorSnooze
	monitorWake
	monitorCancelCountdown
)

type monitorCommand struct {
	Key   monitorKey
	Kind  monitorCommandKind
	Turn  time.Time
	Until time.Time
}

func (e *monitorEntry) command(command monitorCommand, now time.Time) (monitorView, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.keyLocked() != command.Key {
		return monitorView{}, false
	}
	var applied bool
	switch command.Kind {
	case monitorSettle:
		applied = e.settleLocked(command.Turn, now)
	case monitorSnooze:
		applied = e.snoozeLocked(command.Turn, command.Until, now)
	case monitorWake:
		if command.Until.IsZero() {
			command.Until = e.turn.SnoozedUntil
			if command.Until.IsZero() {
				return e.snapshotLocked(), true
			}
		}
		applied = e.wakeLocked(command.Until, now)
	case monitorCancelCountdown:
		e.cancelCountdownLocked()
		applied = true
	}
	if !applied {
		return monitorView{}, false
	}
	return e.viewLocked(), true
}

func (e *monitorEntry) cancelCountdownLocked() {
	removed, _ := e.stopAutoSettleLocked()
	if e.dismissal != autoSettleDismissalOff && !removed {
		e.dismissal = autoSettleDismissalOff
	} else if e.state.State == "working" {
		e.dismissal = autoSettleDismissalCovered
	} else {
		e.dismissal = autoSettleDismissalArmed
	}
}

func (e *monitorEntry) key() monitorKey {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keyLocked()
}
