package attention

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func OpensTurn(state protocol.SessionState) bool {
	switch state {
	case protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateUnknown,
		protocol.SessionStateIdle:
		return true
	default:
		return false
	}
}

func BreaksSnooze(state protocol.SessionState, reason string) bool {
	switch state {
	case protocol.SessionStateUnknown:
		return true
	case protocol.SessionStateIdle:
		return reason == string(sessionstate.ReasonProcessExited)
	default:
		return false
	}
}

type Input struct {
	OpenedAt  time.Time
	SettledAt time.Time

	IsShell bool

	ChiefOfStaff bool

	SessionPinned bool

	// WorkspacePinned and WorkspaceMuted filter at read, not at open: OpenedAt still accumulates, so un-pinning surfaces the turn at its true age.
	WorkspacePinned bool
	WorkspaceMuted  bool
}

func Owed(in Input) bool {
	if Excluded(in) {
		return false
	}
	return in.OpenedAt.After(in.SettledAt)
}

func Excluded(in Input) bool {
	return in.IsShell || in.ChiefOfStaff || in.SessionPinned || in.WorkspacePinned || in.WorkspaceMuted
}
