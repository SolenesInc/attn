package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"pgregory.net/rapid"
)

func sessionInputQuietDeferral(err error) bool {
	var quiet *sessionInputQuietError
	return errors.As(err, &quiet)
}

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

func TestSessionInput_HeartbeatTakenInWaitingDoesNotClaimUserInput(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes [][]byte
	backend.onInput = func(_ string, data []byte) { writes = append(writes, append([]byte(nil), data...)) }
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(sessionID, time.Now()) {
		t.Fatal("fixture did not open a user turn")
	}

	id := inputAttemptID("crew-heartbeat", "generation-1")
	delivery := sessionInputDelivery{
		id: id, sessionID: sessionID, text: crewHeartbeatPrompt,
		origin: maintenanceInput("crew-heartbeat"), placement: sessionInputWhenPromptReady,
	}
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.err != nil || attempt.stage != sessionInputPlaced {
		t.Fatalf("heartbeat placement = %+v, want placed", attempt)
	}
	if len(writes) != 2 {
		t.Fatalf("heartbeat wrote %d PTY chunks, want paste and Enter", len(writes))
	}

	takenAt := time.Now().Add(time.Second)
	effects := d.observePromptTaken(sessionID, crewHeartbeatPrompt, takenAt)
	if effects.receipt == nil || effects.receipt.id != id {
		t.Fatalf("receipt = %+v, want heartbeat %s", effects.receipt, id.String())
	}
	if _, user := d.sessionInputs().currentUserRun(sessionID); user {
		t.Fatal("heartbeat was attributed to the user")
	}
	if got := protocol.Timestamp(protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)).Time(); !got.Equal(takenAt) {
		t.Fatalf("last_model_request_at = %s, want %s", got, takenAt)
	}

	if !d.applyState(sessionStateChange{sessionID: sessionID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("working transition was not applied")
	}
	if _, pending := autoSettlePending(d, sessionID); pending {
		t.Fatal("heartbeat-only run armed auto-settle")
	}
}

func TestSessionInput_UserInputLaterInHeartbeatRunArmsAutoSettle(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(sessionID, time.Now()) {
		t.Fatal("fixture did not open a user turn")
	}

	heartbeatID := inputAttemptID("crew-heartbeat", "generation-1")
	delivery := sessionInputDelivery{
		id: heartbeatID, sessionID: sessionID, text: crewHeartbeatPrompt,
		origin: maintenanceInput("crew-heartbeat"), placement: sessionInputWhenPromptReady,
	}
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("place heartbeat: %v", attempt.err)
	}
	d.observePromptTaken(sessionID, crewHeartbeatPrompt, time.Now())
	if !d.applyState(sessionStateChange{sessionID: sessionID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("working transition was not applied")
	}
	if _, pending := autoSettlePending(d, sessionID); pending {
		t.Fatal("heartbeat armed auto-settle before the user spoke")
	}

	if err := d.writeSessionPTY(sessionID, []byte("the actual answer\r"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	d.observePromptTaken(sessionID, "the actual answer", time.Now())
	if _, pending := autoSettlePending(d, sessionID); !pending {
		t.Fatal("positive user input in the same working run did not arm auto-settle")
	}
}

func TestSessionInput_MaintenanceNudgeLaterInHeartbeatRunDoesNotArmAutoSettle(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(sessionID, time.Now()) {
		t.Fatal("fixture did not open a user turn")
	}

	heartbeat := maintenanceSessionInput("crew-heartbeat", "generation-1", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	if attempt := d.sessionInputs().try(context.Background(), heartbeat); attempt.err != nil {
		t.Fatalf("place heartbeat: %v", attempt.err)
	}
	d.observePromptTaken(sessionID, crewHeartbeatPrompt, time.Now())
	if !d.applyState(sessionStateChange{sessionID: sessionID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("working transition was not applied")
	}

	nudge := maintenanceSessionInput("ticket-nudge", "cursor-7", sessionID, "a ticket needs you", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), nudge); attempt.err != nil {
		t.Fatalf("place ticket nudge: %v", attempt.err)
	}
	d.observePromptTaken(sessionID, nudge.text, time.Now())
	if _, pending := autoSettlePending(d, sessionID); pending {
		t.Fatal("maintenance plus maintenance was mistaken for user conversation input")
	}
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

func TestSessionInput_ConsumedUserControlReleasesComposerGuardWithoutUserCredit(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStatePendingApproval)
	if err := d.writeSessionPTY(sessionID, []byte("y"), "user"); err != nil {
		t.Fatalf("approval key: %v", err)
	}
	d.sessionInputs().observePhase(sessionID, protocol.SessionStateWorking)
	if _, credited := d.sessionInputs().currentUserRun(sessionID); credited {
		t.Fatal("an approval key granted user-conversation credit")
	}
	d.sessionInputs().observePhase(sessionID, protocol.SessionStateWaitingInput)
	d.store.UpdateState(sessionID, protocol.StateWaitingInput)
	backend.screen = "❯"
	delivery := maintenanceSessionInput("crew-heartbeat", "generation-after-approval", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("input after consumed approval key: %v", attempt.err)
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
		deliveryWithText := func(text string) (sessionInputDelivery, bool) {
			for _, delivery := range deliveries {
				if delivery.text == text {
					return delivery, true
				}
			}
			return sessionInputDelivery{}, false
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
				if delivery, ok := deliveryWithText(prompt); placed && ok &&
					((effects.receipt != nil && effects.receipt.id == delivery.id) || (effects.taken != nil && effects.taken.inputID == delivery.id.String())) {
					untaken = ""
				}
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

func TestSessionInput_QuietWindowReleasesTheComposerWithoutAPrompt(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		time.Sleep(sessionInputQuietWindow / 2)
		delivery := maintenanceSessionInput("crew-heartbeat", "mid-window", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		attempt := d.sessionInputs().try(context.Background(), delivery)
		var quiet *sessionInputQuietError
		if !errors.As(attempt.err, &quiet) || !errors.Is(attempt.err, errSessionInputComposerDirty) {
			t.Fatalf("mid-window error = %v, want the quiet-window deferral", attempt.err)
		}
		if quiet.retryAfter != sessionInputQuietWindow/2 {
			t.Fatalf("retryAfter = %v, want the rest of the window %v", quiet.retryAfter, sessionInputQuietWindow/2)
		}
		time.Sleep(quiet.retryAfter)
		delivery = maintenanceSessionInput("crew-heartbeat", "after-window", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
			t.Fatalf("input after the quiet window: %v", attempt.err)
		}
	})
}

const quiesceWatcherTripwire = 20 * transcriptPollInterval

func quiesceTranscriptWatchers(t *testing.T, d *Daemon) {
	t.Helper()
	d.watchersMu.Lock()
	watchers := make([]*transcriptWatcher, 0, len(d.transcriptWatch))
	for _, watcher := range d.transcriptWatch {
		watchers = append(watchers, watcher)
	}
	d.transcriptWatch = make(map[string]*transcriptWatcher)
	d.watchersMu.Unlock()
	for _, watcher := range watchers {
		close(watcher.stopCh)
	}
	for _, watcher := range watchers {
		select {
		case <-watcher.doneCh:
		case <-time.After(quiesceWatcherTripwire):
			t.Fatalf("transcript watcher for %s did not stop within %s", watcher.sessionID, quiesceWatcherTripwire)
		}
	}
}

func settleResend(t *testing.T) {
	t.Helper()
	synctest.Wait()
	time.Sleep(sessionInputSubmitDelay + sessionInputTakenWindow)
	synctest.Wait()
}

func retryEntry(d *Daemon, sessionID string, id sessionInputAttemptID) *sessionInputRetry {
	m := d.sessionInputs()
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return nil
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.retries[id.String()]
}

func laneFor(d *Daemon, sessionID string) *sessionInputLane {
	m := d.sessionInputs()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lanes[sessionID]
}

func autoSettlePending(d *Daemon, sessionID string) (*autoSettleTimer, bool) {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	entry, ok := d.autoSettleTimers[sessionID]
	return entry, ok
}
