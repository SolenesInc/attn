package daemon

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/hostsession"
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

func TestSessionInput_RetryPressesEnterWithoutRepasting(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes [][]byte
	backend.onInput = func(_ string, data []byte) { writes = append(writes, append([]byte(nil), data...)) }
	delivery := sessionInputDelivery{
		id: inputAttemptID("agent-message", "message-1"), sessionID: sessionID, text: "hello",
		origin: maintenanceInput("retry-test"), placement: sessionInputAtTurnBoundary,
	}
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("first attempt: %v", attempt.err)
	}
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("retry: %v", attempt.err)
	}
	if len(writes) != 3 {
		t.Fatalf("writes = %q, want paste, Enter, Enter", writes)
	}
	if string(writes[2]) != "\r" {
		t.Fatalf("retry wrote %q, want Enter only", writes[2])
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

func TestSessionInput_RejectsOneAttemptIDWithDifferentContent(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes int
	backend.onInput = func(_ string, _ []byte) { writes++ }
	delivery := maintenanceSessionInput("ticket-nudge", "cursor-1", sessionID, "first", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("first placement: %v", attempt.err)
	}
	delivery.text = "different"
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.reason != sessionInputReasonIDConflict || attempt.stage != sessionInputIndeterminate {
		t.Fatalf("conflicting identity = %+v, want Indeterminate/IDConflict", attempt)
	}
	if writes != 2 {
		t.Fatalf("identity conflict mutated target: got %d writes, want original paste and Enter", writes)
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

func TestSessionInput_AutomationFailsClosedOnUnknownScreenOrDirtyComposer(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	delivery := sessionInputDelivery{
		id: inputAttemptID("crew-heartbeat", "generation-1"), sessionID: sessionID, text: crewHeartbeatPrompt,
		origin: maintenanceInput("crew-heartbeat"), placement: sessionInputWhenPromptReady,
	}
	backend.screenUnavailable = true
	if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputScreenUnavailable) {
		t.Fatalf("unknown screen error = %v", attempt.err)
	}

	backend.screen = "❯"
	backend.screenUnavailable = false
	if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	delivery.id = inputAttemptID("crew-heartbeat", "generation-2")
	if attempt := d.sessionInputs().try(context.Background(), delivery); !errors.Is(attempt.err, errSessionInputComposerDirty) {
		t.Fatalf("dirty composer error = %v", attempt.err)
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

func TestSessionInput_DifferentAttemptCannotEnterAnUnresolvedComposer(t *testing.T) {
	d, backend, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	var writes [][]byte
	backend.onInput = func(_ string, data []byte) { writes = append(writes, append([]byte(nil), data...)) }
	first := maintenanceSessionInput("ticket-nudge", "cursor-1", sessionID, "first", sessionInputAtTurnBoundary)
	second := maintenanceSessionInput("present-handback", "round-2", sessionID, "second", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), first); attempt.err != nil {
		t.Fatalf("first attempt: %v", attempt.err)
	}
	if attempt := d.sessionInputs().try(context.Background(), second); !errors.Is(attempt.err, errSessionInputComposerOccupied) {
		t.Fatalf("second attempt error = %v, want unresolved-composer deferral", attempt.err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %q, want only the first paste and Enter", writes)
	}
}

func TestSessionInput_ReleasedPlacementRetainsReceiptAndComposerLease(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	first := userConversationSessionInput("request-1", sessionID, "the user's feedback", sessionInputAtTurnBoundary)
	second := maintenanceSessionInput("present-handback", "round-2", sessionID, "second", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), first); attempt.err != nil {
		t.Fatalf("first attempt: %v", attempt.err)
	}
	d.sessionInputs().release(sessionID, first.id)
	if attempt := d.sessionInputs().try(context.Background(), second); !errors.Is(attempt.err, errSessionInputComposerOccupied) {
		t.Fatalf("second attempt error = %v, want released placement to retain its lease", attempt.err)
	}
	effects := d.observePromptTaken(sessionID, first.text, time.Now())
	if effects.taken == nil || effects.taken.origin.kind != sessionInputOriginUserConversation {
		t.Fatalf("late receipt effects = %+v, want user-conversation take", effects)
	}
	if _, credited := d.sessionInputs().currentUserRun(sessionID); !credited {
		t.Fatal("late receipt after release did not grant exact user credit")
	}
	if attempt := d.sessionInputs().lookup(sessionID, first.id); attempt.reason != sessionInputReasonGone {
		t.Fatalf("released taken attempt = %+v, want it cleaned up", attempt)
	}
	if attempt := d.sessionInputs().try(context.Background(), second); attempt.err != nil {
		t.Fatalf("second attempt after receipt: %v", attempt.err)
	}
}

func TestSessionInput_SameTextFromUserAndAutomationIsIndeterminate(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	delivery := maintenanceSessionInput("ticket-nudge", "cursor-1", sessionID, "same words", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("place maintenance input: %v", attempt.err)
	}
	if err := d.writeSessionPTY(sessionID, []byte("same words\r"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	effects := d.observePromptTaken(sessionID, "same words", time.Now())
	if effects.taken != nil || effects.receipt != nil {
		t.Fatalf("ambiguous observation produced provenance: %+v", effects)
	}
	if attempt := d.sessionInputs().lookup(sessionID, delivery.id); attempt.stage != sessionInputIndeterminate {
		t.Fatalf("maintenance attempt stage = %v, want indeterminate", attempt.stage)
	}
	if _, user := d.sessionInputs().currentUserRun(sessionID); user {
		t.Fatal("ambiguous same-text observation granted user credit")
	}
}

func TestSessionInput_HostReceiptIsFencedToTheCurrentRuntime(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	if !d.store.BeginAgentDriverRun(sessionID, "attn-pi", "run-current") {
		t.Fatal("begin current host run")
	}
	delivery := maintenanceSessionInput("host-test", "input-1", sessionID, "hello", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("place input: %v", attempt.err)
	}
	d.handleHostEvent(hostsession.Event{
		SessionID: sessionID, LifecycleID: "run-stale", Seq: 1, Kind: "input_taken",
		Body: map[string]interface{}{"input_id": delivery.id.String()},
	})
	if attempt := d.sessionInputs().lookup(sessionID, delivery.id); attempt.stage != sessionInputPlaced {
		t.Fatalf("stale receipt moved attempt to %v, want placed", attempt.stage)
	}
	d.handleHostEvent(hostsession.Event{
		SessionID: sessionID, LifecycleID: "run-current", Seq: 1, Kind: "input_taken",
		Body: map[string]interface{}{"input_id": delivery.id.String()},
	})
	if attempt := d.sessionInputs().lookup(sessionID, delivery.id); attempt.stage != sessionInputTaken {
		t.Fatalf("current receipt moved attempt to %v, want taken", attempt.stage)
	}
	requestAt := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)
	d.handleHostEvent(hostsession.Event{
		SessionID: sessionID, LifecycleID: "run-current", Seq: 2, Kind: "input_taken",
		Body: map[string]interface{}{"input_id": delivery.id.String()},
	})
	if got := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt); got != requestAt {
		t.Fatalf("duplicate receipt moved request clock from %s to %s", requestAt, got)
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
		var writes [][]byte
		backend.onInput = func(_ string, data []byte) {
			writes = append(writes, append([]byte(nil), data...))
		}
		delivery := maintenanceSessionInput("property", "attempt", sessionID, "maintenance words", sessionInputAtTurnBoundary)
		pastesAtLifetimeStart := 0
		userCredited := false

		countPastes := func() int {
			count := 0
			for _, write := range writes {
				if strings.HasPrefix(string(write), sessionInputPasteStart) && strings.Contains(string(write), delivery.text) {
					count++
				}
			}
			return count
		}
		assertContracts := func(rt *rapid.T) {
			if got := countPastes() - pastesAtLifetimeStart; got > 1 {
				rt.Fatalf("one attempt lifetime pasted %d copies; writes=%q", got, writes)
			}
			_, gotCredit := d.sessionInputs().currentUserRun(sessionID)
			if gotCredit != userCredited {
				rt.Fatalf("user credit=%v, want %v after writes=%q", gotCredit, userCredited, writes)
			}
		}

		rt.Repeat(map[string]func(*rapid.T){
			"try": func(rt *rapid.T) {
				d.sessionInputs().try(context.Background(), delivery)
			},
			"write": func(rt *rapid.T) {
				data := rapid.SampledFrom([]string{"x", "user answer\r", "\r"}).Draw(rt, "data")
				if err := d.writeSessionPTY(sessionID, []byte(data), "user"); err != nil {
					rt.Fatalf("write user input: %v", err)
				}
			},
			"observe_taken": func(rt *rapid.T) {
				prompt := rapid.SampledFrom([]string{delivery.text, "user answer", "unrelated"}).Draw(rt, "prompt")
				effects := d.observePromptTaken(sessionID, prompt, time.Now())
				if effects.taken != nil && effects.taken.origin.kind == sessionInputOriginUserConversation {
					userCredited = true
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
			},
			"replace_runtime": func(rt *rapid.T) {
				d.sessionInputs().forgetSession(sessionID)
				pastesAtLifetimeStart = countPastes()
				userCredited = false
			},
			"release": func(rt *rapid.T) {
				d.sessionInputs().release(sessionID, delivery.id)
				pastesAtLifetimeStart = countPastes()
			},
			"": assertContracts,
		})
	})
}

func TestSessionInput_MouseReportsDoNotGuardTheComposer(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	for _, report := range []string{"\x1b[<35;12;20M", "\x1b[<0;32;26M\x1b[<3;32;26m", "\x1b[I", "\x1b[O"} {
		if err := d.writeSessionPTY(sessionID, []byte(report), "user"); err != nil {
			t.Fatalf("mouse input: %v", err)
		}
	}
	delivery := maintenanceSessionInput("crew-heartbeat", "after-mouse", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("a mouse move guarded the composer: %v", attempt.err)
	}
}

func TestSessionInput_TaggedPointerAndResponseDoNotGuardTheComposer(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	lane := d.sessionInputs().lane(sessionID)

	for _, tagged := range []struct {
		data   string
		source string
	}{
		{"\x1b[M !!", "pointer"},
		{"\x1b[<0;1;1M", "pointer"},
		{"\x1b[0n", "response"},
	} {
		if err := d.writeSessionPTY(sessionID, []byte(tagged.data), tagged.source); err != nil {
			t.Fatalf("%s input: %v", tagged.source, err)
		}
	}
	lane.mu.Lock()
	generation := lane.userGeneration
	lane.mu.Unlock()
	if generation != 0 {
		t.Fatalf("lane user generation = %d, want 0 (tagged input is not a keystroke)", generation)
	}

	delivery := maintenanceSessionInput("crew-heartbeat", "after-tagged", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("tagged pointer/response input guarded the composer: %v", attempt.err)
	}
}

func TestSessionInput_UntaggedX10MouseReportStillGuardsTheComposer(t *testing.T) {
	d, _, sessionID := newSessionInputDaemon(t, protocol.SessionStateWaitingInput)
	if err := d.writeSessionPTY(sessionID, []byte("\x1b[M !!"), "user"); err != nil {
		t.Fatalf("mouse input: %v", err)
	}
	delivery := maintenanceSessionInput("crew-heartbeat", "after-x10", sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if !errors.Is(attempt.err, errSessionInputComposerDirty) {
		t.Fatalf("untagged X10 report error = %v, want the composer-dirty deferral", attempt.err)
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

// A watcher returns at the top of its poll select: 20 runs stopped under 10us.
const quiesceWatcherTripwire = 20 * transcriptPollInterval

// A channel made inside a bubble is fatal to send on from outside it, and the
// store's pool is shared, so no watcher may query it during one.
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

// Leaving the bubble while a resend is still placing is a synctest deadlock.
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
