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

func OpensTurnAfterNotification(state protocol.SessionState) bool {
	return state == protocol.SessionStatePendingApproval || state == protocol.SessionStateUnknown
}

func TurnByAgent(opened, settled, agentAt time.Time) bool {
	latest := settled
	if opened.After(settled) {
		latest = opened
	}
	return !agentAt.IsZero() && agentAt.Equal(latest)
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
}

func Excluded(in Input) bool {
	return in.IsShell || in.ChiefOfStaff
}
