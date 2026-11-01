package sessionmonitor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type inputOnlyHost struct {
	Host
	ctx       context.Context
	submitted int
}

func (h *inputOnlyHost) Context() context.Context                  { return h.ctx }
func (h *inputOnlyHost) HomeFor(*Monitor) Home                     { return nil }
func (h *inputOnlyHost) Link(*Monitor, harness.Voice) harness.Link { return nil }
func (h *inputOnlyHost) ViewOwner(harness.TerminalID) string       { return "" }
func (h *inputOnlyHost) LastKey(harness.TerminalID) time.Time      { return time.Time{} }
func (h *inputOnlyHost) Input(context.Context, harness.TerminalID, []byte) error {
	h.submitted++
	return nil
}

func TestAFullDeliveryHistoryRefusesNewInputAndStillAnswersRetries(t *testing.T) {
	previousDelay := SubmitDelay
	SubmitDelay = 0
	t.Cleanup(func() { SubmitDelay = previousDelay })
	host := &inputOnlyHost{ctx: t.Context()}
	monitor := NewRegistry(host, nil).Start(nil, Start{
		Session:  "shop",
		Terminal: "shop",
		Launch:   func() Launch { return Launch{Known: true, State: protocol.SessionStateIdle} },
	})
	deliver := func(id string) Attempt {
		return monitor.Place(t.Context(), MaintenanceInput("test", id, "Use blue for the banner", AtTurnBoundary))
	}

	for n := range MaxPlacedDeliveries {
		if attempt := deliver(fmt.Sprint(n)); attempt.Stage != Placed {
			t.Fatalf("delivery %d refused before the limit: %+v", n, attempt)
		}
	}
	over := deliver("over-budget")
	if over.Stage != Failed || over.Err == nil {
		t.Fatalf("new delivery at capacity = %+v, want a failure", over)
	}
	for _, detail := range []string{"shop", fmt.Sprintf("max_placed_deliveries=%d", MaxPlacedDeliveries), fmt.Sprintf("asked for %d", MaxPlacedDeliveries+1)} {
		if !strings.Contains(over.Err.Error(), detail) {
			t.Errorf("refusal %q omits %q", over.Err, detail)
		}
	}
	submittedAtCapacity := host.submitted
	for _, id := range []string{"0", fmt.Sprint(MaxPlacedDeliveries - 1)} {
		if retry := deliver(id); retry.Stage != Placed {
			t.Fatalf("retry of %s at capacity = %+v, want its original receipt", id, retry)
		}
	}
	if host.submitted != submittedAtCapacity {
		t.Fatalf("retries and the refusal wrote %d extra inputs, want none", host.submitted-submittedAtCapacity)
	}
}

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
			promptReady := PhaseAllows(WhenPromptReady, state)
			wantPromptReady := state == protocol.SessionStateIdle || state == protocol.SessionStateWaitingInput
			if promptReady != wantPromptReady {
				t.Fatalf("WhenPromptReady(%s)=%v, want %v", state, promptReady, wantPromptReady)
			}
			workingOrPromptReady := PhaseAllows(WhenWorkingOrPromptReady, state)
			wantWorkingOrPromptReady := wantPromptReady || state == protocol.SessionStateWorking
			if workingOrPromptReady != wantWorkingOrPromptReady {
				t.Fatalf("WhenWorkingOrPromptReady(%s)=%v, want %v", state, workingOrPromptReady, wantWorkingOrPromptReady)
			}
			turnBoundary := PhaseAllows(AtTurnBoundary, state)
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
		if got := IsComposerKeystroke(tc.source, []byte(tc.data)); got != tc.guards {
			t.Errorf("%s (%s %q) guards the composer = %v, want %v", tc.name, tc.source, tc.data, got, tc.guards)
		}
	}
}
