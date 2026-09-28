package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"pgregory.net/rapid"
)

func newSessionInputDaemon(t *testing.T, state protocol.SessionState) (*Daemon, *fakeSpawnBackend, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "session-input.sock"))
	backend := &fakeSpawnBackend{screen: "❯"}
	d.ptyBackend = backend
	id := "session-input"
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude, Directory: t.TempDir(),
		State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	return d, backend, id
}

func TestSessionInput_RetryCannotAnswerANewApproval(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes [][]byte
	backend.onInput = func(_ string, data []byte) { writes = append(writes, append([]byte(nil), data...)) }
	delivery := maintenanceSessionInput("ticket-nudge", "cursor-approval", sessionID, "first", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("first attempt: %v", attempt.err)
	}
	if !d.store.UpdateState(sessionID, protocol.StatePendingApproval) {
		t.Fatal("move session to pending approval")
	}
	if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputBlockedByApproval) {
		t.Fatalf("retry error = %v, want approval refusal", attempt.err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %q, want no retry Enter after approval opened", writes)
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
			_, promptReady := deliveryAllowedForPhase(sessionInputWhenPromptReady, state)
			wantPromptReady := state == protocol.SessionStateIdle || state == protocol.SessionStateWaitingInput
			if promptReady != wantPromptReady {
				t.Fatalf("WhenPromptReady(%s)=%v, want %v", state, promptReady, wantPromptReady)
			}
			_, turnBoundary := deliveryAllowedForPhase(sessionInputAtTurnBoundary, state)
			wantTurnBoundary := state != protocol.SessionStatePendingApproval
			if turnBoundary != wantTurnBoundary {
				t.Fatalf("AtTurnBoundary(%s)=%v, want %v", state, turnBoundary, wantTurnBoundary)
			}
		})
	}
}

func TestSessionInput_RandomInterleavingsKeepMechanicalAndCausalContracts(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
		var writesMu sync.Mutex
		var writes [][]byte
		backend.onInput = func(_ string, data []byte) {
			writesMu.Lock()
			defer writesMu.Unlock()
			writes = append(writes, append([]byte(nil), data...))
		}
		maintenance := maintenanceSessionInput("property", "attempt", sessionID, "maintenance words", sessionInputAtTurnBoundary)
		reworded := maintenance
		reworded.text = "reworded maintenance"
		other := maintenanceSessionInput("property", "other", sessionID, "other words", sessionInputAtTurnBoundary)
		feedback := userConversationSessionInput("request", sessionID, "the user's feedback", sessionInputAtTurnBoundary)
		deliveries := []sessionInputDelivery{maintenance, reworded, other, feedback}

		pastesAtLifetimeStart := map[string]int{}
		seenPastes := map[string]int{}
		untaken := ""
		typedSincePaste := false
		ambiguous := map[string]bool{}
		userCredited := false

		pastesOf := func(text string) int {
			writesMu.Lock()
			defer writesMu.Unlock()
			count := 0
			for _, write := range writes {
				if string(write) == sessionInputPasteStart+text+sessionInputPasteEnd {
					count++
				}
			}
			return count
		}
		startLifetime := func(text string) {
			pastesAtLifetimeStart[text] = pastesOf(text)
		}
		assertContracts := func(rt *rapid.T) {
			for _, delivery := range deliveries {
				pasted := pastesOf(delivery.text)
				if pasted-pastesAtLifetimeStart[delivery.text] > 1 {
					rt.Fatalf("one lifetime of %s pasted %d copies", delivery.id.String(), pasted-pastesAtLifetimeStart[delivery.text])
				}
				if pasted > seenPastes[delivery.text] {
					if untaken != "" && untaken != delivery.text && !typedSincePaste {
						rt.Fatalf("%q was pasted onto %q before the agent took it", delivery.text, untaken)
					}
					untaken, typedSincePaste = delivery.text, false
					seenPastes[delivery.text] = pasted
				}
			}
			sharedID := pastesOf(maintenance.text) + pastesOf(reworded.text) -
				pastesAtLifetimeStart[maintenance.text] - pastesAtLifetimeStart[reworded.text]
			if sharedID > 1 {
				rt.Fatalf("one lifetime of %s pasted %d texts", maintenance.id.String(), sharedID)
			}
			if _, gotCredit := d.sessionInputs().currentUserRun(sessionID); gotCredit != userCredited {
				rt.Fatalf("user credit=%v, want %v", gotCredit, userCredited)
			}
		}

		rt.Repeat(map[string]func(*rapid.T){
			"try": func(rt *rapid.T) {
				delivery := rapid.SampledFrom(deliveries).Draw(rt, "delivery")
				sibling := ""
				for _, candidate := range deliveries {
					if candidate.id == delivery.id && candidate.text != delivery.text && pastesOf(candidate.text) > pastesAtLifetimeStart[candidate.text] {
						sibling = candidate.text
					}
				}
				writesMu.Lock()
				before := len(writes)
				writesMu.Unlock()
				d.sessionInputs().try(context.Background(), delivery)
				writesMu.Lock()
				after := len(writes)
				writesMu.Unlock()
				if sibling != "" && after != before {
					rt.Fatalf("%s placed %q and then typed into the agent again for %q", delivery.id.String(), sibling, delivery.text)
				}
			},
			"write": func(rt *rapid.T) {
				data := rapid.SampledFrom([]string{"x", "user answer\r", "\r", maintenance.text + "\r", feedback.text + "\r"}).Draw(rt, "data")
				if err := d.writeSessionPTY(sessionID, []byte(data), "user"); err != nil {
					rt.Fatalf("write user input: %v", err)
				}
				if untaken != "" && data == untaken+"\r" && !typedSincePaste {
					ambiguous[untaken] = true
				}
				typedSincePaste = true
			},
			"observe_taken": func(rt *rapid.T) {
				prompt := rapid.SampledFrom([]string{maintenance.text, reworded.text, other.text, feedback.text, "user answer", "unrelated"}).Draw(rt, "prompt")
				placed := prompt == untaken
				typedOver := typedSincePaste
				effects := d.observePromptTaken(sessionID, prompt, time.Now())
				if placed && ambiguous[prompt] && (effects.taken != nil || effects.receipt != nil) {
					rt.Fatalf("the user and attn both submitted %q, yet the take claimed provenance %+v", prompt, effects)
				}
				if placed && !typedOver && prompt == feedback.text &&
					(effects.taken == nil || effects.taken.origin.kind != sessionInputOriginUserConversation) {
					rt.Fatalf("the agent took the user's placed feedback without crediting the user: %+v", effects)
				}
				if effects.taken != nil && effects.taken.origin.kind == sessionInputOriginUserConversation {
					userCredited = true
				}
				if placed {
					untaken = ""
				}
				clear(ambiguous)
			},
			"observe_phase": func(rt *rapid.T) {
				working := rapid.Bool().Draw(rt, "working")
				phase := protocol.SessionStateIdle
				if working {
					phase = protocol.SessionStateWorking
				}
				d.sessionInputs().observePhase(sessionID, phase)
				if !working {
					userCredited = false
				}
				clear(ambiguous)
			},
			"replace_runtime": func(rt *rapid.T) {
				d.sessionInputs().forgetSession(sessionID)
				for _, delivery := range deliveries {
					startLifetime(delivery.text)
				}
				untaken, userCredited = "", false
				clear(ambiguous)
			},
			"release": func(rt *rapid.T) {
				released := rapid.SampledFrom([]sessionInputDelivery{maintenance, feedback}).Draw(rt, "released")
				d.sessionInputs().release(sessionID, released.id)
				clear(ambiguous)
				for _, delivery := range deliveries {
					if delivery.id == released.id {
						startLifetime(delivery.text)
					}
				}
			},
			"": assertContracts,
		})
	})
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
