package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSessionInput_PlacementPhaseContracts(t *testing.T) {
	states := []protocol.SessionState{
		protocol.SessionStateLaunching,
		protocol.SessionStateWorking,
		protocol.SessionStatePendingApproval,
		protocol.SessionStateWaitingInput,
		protocol.SessionStateIdle,
		protocol.SessionStateUnknown,
		protocol.SessionStateScheduled,
		protocol.SessionStateRecoverable,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			promptReady := sessionInputPhaseAllows(sessionInputWhenPromptReady, state)
			wantPromptReady := state == protocol.SessionStateIdle || state == protocol.SessionStateWaitingInput
			if promptReady != wantPromptReady {
				t.Fatalf("WhenPromptReady(%s)=%v, want %v", state, promptReady, wantPromptReady)
			}
			turnBoundary := sessionInputPhaseAllows(sessionInputAtTurnBoundary, state)
			wantTurnBoundary := state != protocol.SessionStatePendingApproval
			if turnBoundary != wantTurnBoundary {
				t.Fatalf("AtTurnBoundary(%s)=%v, want %v", state, turnBoundary, wantTurnBoundary)
			}
		})
	}
}

func TestSessionInput_OnlyWhatTheUserTypesGuardsTheComposer(t *testing.T) {
	for _, tc := range []struct {
		name, source, data string
		guards             bool
	}{
		{"typed text", "user", "half written", true},
		{"an Enter", "user", "\r", true},
		{"an untagged X10 mouse report", "user", "\x1b[M !!", true},
		{"an SGR mouse move", "user", "\x1b[<35;12;20M", false},
		{"an SGR press and release", "user", "\x1b[<0;32;26M\x1b[<3;32;26m", false},
		{"a focus-in report", "user", "\x1b[I", false},
		{"a focus-out report", "user", "\x1b[O", false},
		{"typing after a mouse move", "user", "\x1b[<35;12;20Mx", true},
		{"a tagged X10 pointer report", "pointer", "\x1b[M !!", false},
		{"a tagged SGR pointer report", "pointer", "\x1b[<0;1;1M", false},
		{"a terminal response", "response", "\x1b[0n", false},
		{"input attn typed", "automation", "a delegate reported", false},
		{"an attach replay", "attach_replay", "half written", false},
	} {
		if got := isComposerKeystroke(tc.source, []byte(tc.data)); got != tc.guards {
			t.Errorf("%s (%s %q) guards the composer = %v, want %v", tc.name, tc.source, tc.data, got, tc.guards)
		}
	}
}
