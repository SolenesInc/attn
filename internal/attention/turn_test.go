package attention

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func TestOpensTurn(t *testing.T) {
	tests := []struct {
		state protocol.SessionState
		want  bool
	}{
		{protocol.SessionStateWaitingInput, true},
		{protocol.SessionStatePendingApproval, true},
		{protocol.SessionStateUnknown, true},
		{protocol.SessionStateLaunching, false},
		{protocol.SessionStateWorking, false},
		{protocol.SessionStateScheduled, false},
		{protocol.SessionStateRecoverable, false},
		{protocol.SessionStateIdle, true},
	}

	for _, tt := range tests {
		if got := OpensTurn(tt.state); got != tt.want {
			t.Errorf("OpensTurn(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestBreaksSnooze(t *testing.T) {
	tests := []struct {
		name   string
		state  protocol.SessionState
		reason string
		want   bool
	}{
		{"stuck is the daemon admitting it cannot tell", protocol.SessionStateUnknown, string(sessionstate.ReasonStuck), true},
		{"no evidence, same admission", protocol.SessionStateUnknown, string(sessionstate.ReasonNoEvidence), true},
		{"unknown breaks through whatever the reason", protocol.SessionStateUnknown, "", true},
		{"the agent's process is gone", protocol.SessionStateIdle, string(sessionstate.ReasonProcessExited), true},

		{"a run that merely ended", protocol.SessionStateIdle, string(sessionstate.ReasonClassifierVerdict), false},
		{"a session sitting at its prompt", protocol.SessionStateIdle, string(sessionstate.ReasonAtPrompt), false},
		{"a question", protocol.SessionStateWaitingInput, string(sessionstate.ReasonQuestionOpen), false},
		{"an approval", protocol.SessionStatePendingApproval, string(sessionstate.ReasonApprovalOpen), false},
		{"working", protocol.SessionStateWorking, string(sessionstate.ReasonHeartbeatBusy), false},
		{"scheduled", protocol.SessionStateScheduled, string(sessionstate.ReasonCronPending), false},
		{"recoverable, which the daemon revives unattended", protocol.SessionStateRecoverable, "", false},

		{"a waiting session carrying the exited reason", protocol.SessionStateWaitingInput, string(sessionstate.ReasonProcessExited), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BreaksSnooze(tt.state, tt.reason); got != tt.want {
				t.Errorf("BreaksSnooze(%q, %q) = %v, want %v", tt.state, tt.reason, got, tt.want)
			}
		})
	}
}
