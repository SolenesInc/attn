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

func (e *Entry) Do(command Command, now time.Time) (View, bool) {
	if e == nil {
		return View{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.keyLocked() != command.Key {
		return View{}, false
	}
	var applied bool
	switch command.Kind {
	case Settle:
		applied = e.settleLocked(command.Turn, now)
	case Snooze:
		applied = e.snoozeLocked(command.Turn, command.Until, now)
	case Wake:
		if command.Until.IsZero() {
			command.Until = e.turn.SnoozedUntil
			if command.Until.IsZero() {
				return e.snapshotLocked(), true
			}
		}
		applied = e.wakeLocked(command.Until, now)
	case CancelCountdown:
		e.cancelCountdownLocked()
		applied = true
	}
	if !applied {
		return View{}, false
	}
	return e.viewLocked(), true
}

func (e *Entry) cancelCountdownLocked() {
	removed, _ := e.stopAutoSettleLocked()
	if e.dismissal != autoSettleDismissalOff && !removed {
		e.dismissal = autoSettleDismissalOff
	} else if e.state.State == "working" {
		e.dismissal = autoSettleDismissalCovered
	} else {
		e.dismissal = autoSettleDismissalArmed
	}
}

func (e *Entry) Key() Key {
	if e == nil {
		return Key{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keyLocked()
}

func (e *Entry) Publish(view View) bool { return e.home.Applied(view) }

func (e *Entry) Revision() uint64 {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.revision
}
