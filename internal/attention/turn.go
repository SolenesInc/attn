package attention

import (
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
	IsShell bool

	ChiefOfStaff bool

	SessionPinned bool

	WorkspacePinned bool
	WorkspaceMuted  bool
}

func Excluded(in Input) bool {
	return in.IsShell || in.ChiefOfStaff || in.SessionPinned || in.WorkspacePinned || in.WorkspaceMuted
}
