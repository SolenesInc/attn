package daemon

import (
	"context"
	"errors"
	"net"
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

func TestSessionInput_UserPromptTakenAfterWorkingTransitionArmsAutoSettle(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(sessionID, time.Now()) {
		t.Fatal("fixture did not open a user turn")
	}

	if err := d.writeSessionPTY(sessionID, []byte("the actual answer\r"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	if !d.applyState(sessionStateChange{sessionID: sessionID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("working transition was not applied")
	}
	d.observePromptTaken(sessionID, "the actual answer", time.Now())
	if _, pending := autoSettlePending(d, sessionID); !pending {
		t.Fatal("working-before-hook ordering lost the user input that arms auto-settle")
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

func TestSessionInput_IndeterminateComposerRetryBecomesPlacedOnlyAfterEnterSucceeds(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes [][]byte
	enterFailures := 1
	backend.onInput = func(_ string, data []byte) { writes = append(writes, append([]byte(nil), data...)) }
	backend.onInputResult = func(_ string, data []byte) error {
		if string(data) == "\r" && enterFailures > 0 {
			enterFailures--
			return errors.New("uncertain Enter write")
		}
		return nil
	}
	delivery := maintenanceSessionInput("ticket-nudge", "cursor-transport", sessionID, "first", sessionInputAtTurnBoundary)
	first := d.sessionInputs().try(context.Background(), delivery)
	if first.err == nil || first.stage != sessionInputIndeterminate {
		t.Fatalf("first attempt = %+v, want transport error and Indeterminate", first)
	}
	retry := d.sessionInputs().try(context.Background(), delivery)
	if retry.err != nil || retry.stage != sessionInputPlaced {
		t.Fatalf("retry = %+v, want successful Enter to restore Placed", retry)
	}
	if len(writes) != 3 || string(writes[2]) != "\r" {
		t.Fatalf("writes = %q, want paste, failed Enter, retry Enter", writes)
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

func TestSessionInput_OnlyMarkedPromptSubmitGrantsUserCredit(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWorking)
	before := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)

	callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{
			ID: sessionID, State: protocol.StateWorking, Prompt: protocol.Ptr("ordinary tool hook"),
		})
	})
	if _, credited := d.sessionInputs().currentUserRun(sessionID); credited {
		t.Fatal("a generic working hook with prompt text granted user credit")
	}
	if got := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt); got != before {
		t.Fatalf("generic hook moved request clock from %q to %q", before, got)
	}

	if err := d.writeSessionPTY(sessionID, []byte("the user's answer\r"), "user"); err != nil {
		t.Fatalf("write user input: %v", err)
	}
	callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{
			ID: sessionID, State: protocol.StateWorking,
			HookEvent: protocol.Ptr("user_prompt_submit"), Prompt: protocol.Ptr("the user's answer"),
		})
	})
	if _, credited := d.sessionInputs().currentUserRun(sessionID); !credited {
		t.Fatal("a positively marked UserPromptSubmit did not grant user credit")
	}
	if got := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt); got == "" || got == before {
		t.Fatalf("UserPromptSubmit left request clock at %q", got)
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

func armRetry(t *testing.T, d *Daemon, sessionID string, resend func()) (sessionInputAttemptID, *sessionInputRetry) {
	t.Helper()
	delivery := maintenanceSessionInput("crew-heartbeat", "generation-1", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	delivery.resend = resend
	if attempt := d.sessionInputs().try(context.Background(), delivery); !sessionInputQuietDeferral(attempt.err) {
		t.Fatalf("delivery into a typed-in composer = %v, want the quiet-window deferral", attempt.err)
	}
	return delivery.id, retryEntry(d, sessionID, delivery.id)
}

func TestSessionInputRetryStaleCallbackLeavesItsReplacementArmed(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		resends := make(chan string, 4)
		id, stale := armRetry(t, d, sessionID, func() { resends <- "stale" })
		if stale == nil {
			t.Fatal("the held delivery armed no retry")
		}
		_, replacement := armRetry(t, d, sessionID, func() { resends <- "replacement" })
		if replacement == nil || replacement == stale {
			t.Fatalf("re-arming kept the old entry: %p", replacement)
		}

		d.sessionInputs().fireRetry(sessionID, id.String(), stale)
		if current := retryEntry(d, sessionID, id); current != replacement {
			t.Fatalf("a stale callback evicted the replacement: %p", current)
		}
		if len(resends) != 0 {
			t.Fatalf("a stale callback resent %q", <-resends)
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if len(resends) != 1 {
			t.Fatalf("the replacement resent %d times, want once", len(resends))
		}
		if got := <-resends; got != "replacement" {
			t.Fatalf("resend came from %q", got)
		}
	})
}

func TestSessionInputRetryRefusesToArmAfterStop(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		resends := make(chan string, 2)
		armRetry(t, d, sessionID, func() { resends <- "before stop" })

		close(d.done)
		d.sessionInputs().stopRetries()
		delivery := maintenanceSessionInput("crew-heartbeat", "after-stop", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		delivery.resend = func() { resends <- "after stop" }
		if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputLaneClosed) {
			t.Fatalf("delivery after stop = %v, want the closed lane", attempt.err)
		}
		if armed := retryEntry(d, sessionID, delivery.id); armed != nil {
			t.Fatalf("a retry armed after stop for %s", delivery.id)
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if len(resends) != 0 {
			t.Fatalf("a retry fired after stop: %q", <-resends)
		}
	})
}

func TestSessionInputRetriesCollidingOnTheComposerBothLand(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var mu sync.Mutex
	var landed []string
	collisions := 0
	backend.onInput = func(_ string, data []byte) {}
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		var send func(key, text string)
		send = func(key, text string) {
			delivery := maintenanceSessionInput("collide", key, sessionID, text, sessionInputWhenPromptReady)
			delivery.resend = func() { send(key, text) }
			attempt := d.sessionInputs().try(context.Background(), delivery)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case attempt.err == nil:
				landed = append(landed, text)
				d.sessionInputs().release(sessionID, delivery.id)
			case errors.Is(attempt.err, errSessionInputComposerOccupied), errors.Is(attempt.err, errSessionInputPlacingAnother):
				collisions++
			}
		}
		send("first", "first prompt")
		send("second", "second prompt")

		time.Sleep(sessionInputQuietWindow)
		settleResend(t)
		mu.Lock()
		first, saw := append([]string(nil), landed...), collisions
		mu.Unlock()
		if len(first) != 1 {
			t.Fatalf("prompts landed with the composer held = %v, want one", first)
		}
		if saw == 0 {
			t.Fatal("the second resend never collided on the occupied composer")
		}

		d.observePromptTaken(sessionID, first[0], time.Now())
		time.Sleep(sessionInputComposerRetry)
		settleResend(t)
		mu.Lock()
		defer mu.Unlock()
		if len(landed) != 2 {
			t.Fatalf("prompts landed = %v, want both", landed)
		}
	})
}

func TestSessionInputStopRetriesWaitsForAResendAlreadyRunning(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		entered, finish := make(chan struct{}), make(chan struct{})
		id, entry := armRetry(t, d, sessionID, func() {
			close(entered)
			<-finish
		})
		if entry == nil {
			t.Fatal("the held delivery armed no retry")
		}

		go d.sessionInputs().fireRetry(sessionID, id.String(), entry)
		<-entered
		stopped := make(chan struct{})
		go func() {
			d.sessionInputs().stopRetries()
			close(stopped)
		}()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("stop returned while a resend was still running")
		default:
		}

		close(finish)
		<-stopped
	})
}

func TestSessionInputRetryEnteringAfterStopDoesNotResend(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		resends := make(chan string, 2)
		id, entry := armRetry(t, d, sessionID, func() { resends <- "after stop" })
		if entry == nil {
			t.Fatal("the held delivery armed no retry")
		}

		d.sessionInputs().stopRetries()
		d.sessionInputs().fireRetry(sessionID, id.String(), entry)
		if len(resends) != 0 {
			t.Fatalf("a callback that reached the lane after stop resent %q", <-resends)
		}
	})
}

func laneFor(d *Daemon, sessionID string) *sessionInputLane {
	m := d.sessionInputs()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lanes[sessionID]
}

func TestSessionInputRetryCannotResendThroughAReplacedLane(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var resendErr error
		id, entry := armRetry(t, d, sessionID, func() {
			close(entered)
			<-release
			resend := maintenanceSessionInput("crew-sleep", "after-replace", sessionID, crewSleepPrompt, sessionInputAtTurnBoundary)
			resendErr = d.sessionInputs().try(context.Background(), resend).err
		})
		if entry == nil {
			t.Fatal("the held delivery armed no retry")
		}
		original := laneFor(d, sessionID)

		go d.sessionInputs().fireRetry(sessionID, id.String(), entry)
		<-entered
		forgotten := make(chan struct{})
		go func() {
			d.sessionInputs().forgetSession(sessionID)
			close(forgotten)
		}()
		synctest.Wait()
		select {
		case <-forgotten:
			t.Fatal("the replacement completed while a resend was still running")
		default:
		}

		close(release)
		<-forgotten
		if !errors.Is(resendErr, errSessionInputLaneClosed) {
			t.Fatalf("a resend from the replaced runtime returned %v, want the closed lane", resendErr)
		}
		if current := laneFor(d, sessionID); current != nil && current != original {
			t.Fatal("the resend placed through a lane created for the replacement")
		}
	})
}
