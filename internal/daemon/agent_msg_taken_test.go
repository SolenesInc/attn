package daemon

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// withAgentMessageTakenWindow restores confirmation for one test; TestMain zeroes it
// so the fake PTY, which never reports working, does not time out every delivery.
func withAgentMessageTakenWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := sessionInputTakenWindow
	sessionInputTakenWindow = window
	t.Cleanup(func() { sessionInputTakenWindow = previous })
}

func TestAgentMsgQueuesWhenTheTargetNeverTakesIt(t *testing.T) {
	withAgentMessageTakenWindow(t, 50*time.Millisecond)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "did the migration land?")
	result := resp.AgentMsgResult
	if result == nil || result.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("result = %+v, want queued", result)
	}
	if !strings.Contains(result.Detail, "did not start a turn") {
		t.Fatalf("detail does not say what happened: %q", result.Detail)
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1: %q", len(prompts), prompts)
	}

	queued, err := d.store.UndeliveredAgentMessages("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 {
		t.Fatalf("unconfirmed message is not queued for the drain: %+v", queued)
	}
}

func TestAgentMsgRedeliveryPressesEnterRatherThanRepasting(t *testing.T) {
	withAgentMessageTakenWindow(t, 50*time.Millisecond)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "the epic is green")
	if result := resp.AgentMsgResult; result == nil || result.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("result = %+v, want queued", result)
	}

	prompts := doorbell.pasted()
	if len(prompts) != 1 {
		t.Fatalf("pasted %d prompts before retry, want 1: %q", len(prompts), prompts)
	}
	retried := make(chan struct{})
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		if string(data) == "\r" {
			select {
			case <-retried:
			default:
				close(retried)
			}
		}
	}}
	drained := make(chan int, 1)
	d.agentMessageDrainHook = func(_ string, delivered int) { drained <- delivered }
	go func() {
		<-retried
		d.observePromptTaken("target-session-id", prompts[0], time.Now())
	}()
	if !d.applyState(sessionStateChange{
		sessionID: "target-session-id",
		state:     protocol.StateWorking,
		cause:     liveSignal{},
	}) {
		t.Fatal("applyState did not apply")
	}

	select {
	case delivered := <-drained:
		if delivered != 1 {
			t.Fatalf("drain delivered %d, want 1", delivered)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain never ran")
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1 — the redelivery retyped the message: %q", len(prompts), prompts)
	}
}

func TestAgentMsgTakenReceiptCoalescesRacingStateChangeDrain(t *testing.T) {
	d, recorder := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	synctest.Test(t, func(t *testing.T) {
		withAgentMessageTakenWindow(t, 2*time.Second)
		typed := make(chan string, 1)
		record := recorder.backend().onInput
		d.ptyBackend = &fakeSpawnBackend{onInput: func(sessionID string, data []byte) {
			record(sessionID, data)
			text := string(data)
			if sessionID == "target-session-id" && strings.HasPrefix(text, sessionInputPasteStart) && strings.HasSuffix(text, sessionInputPasteEnd) {
				prompt := strings.TrimSuffix(strings.TrimPrefix(text, sessionInputPasteStart), sessionInputPasteEnd)
				select {
				case typed <- prompt:
				default:
				}
			}
		}}
		go func() {
			prompt := <-typed
			d.applyState(sessionStateChange{
				sessionID: "target-session-id",
				state:     protocol.StateWorking,
				cause:     liveSignal{},
			})
			d.observePromptTaken("target-session-id", prompt, time.Now())
		}()

		resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "rebase when you surface")
		result := resp.AgentMsgResult
		if result == nil || result.Status != protocol.AgentMsgStatusDelivered {
			t.Fatalf("result = %+v, want delivered", result)
		}
		queued, err := d.store.UndeliveredAgentMessages("target-session-id")
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) != 0 {
			t.Fatalf("a confirmed delivery is still queued: %+v", queued)
		}
		synctest.Wait()
		if prompts := recorder.pasted(); len(prompts) != 1 {
			t.Fatalf("live send and state-change drain pasted %d copies, want 1: %q", len(prompts), prompts)
		}
	})
}

func TestAgentMsgToAWorkingTargetUsesPromptReceiptWithoutAStateEdge(t *testing.T) {
	withAgentMessageTakenWindow(t, 2*time.Second)
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWorking)

	typed := make(chan struct{})
	record := doorbell.backend().onInput
	d.ptyBackend = &fakeSpawnBackend{onInput: func(sessionID string, data []byte) {
		record(sessionID, data)
		select {
		case <-typed:
		default:
			close(typed)
		}
	}}
	done := make(chan protocol.Response, 1)
	go func() { done <- callAgentMsg(t, d, "target-session-id", "sender-session-id", "when you land, ping me") }()
	<-typed
	prompts := doorbell.pasted()
	if len(prompts) != 1 {
		t.Fatalf("pasted %d prompts, want 1: %q", len(prompts), prompts)
	}
	d.observePromptTaken("target-session-id", prompts[0], time.Now())

	select {
	case resp := <-done:
		result := resp.AgentMsgResult
		if result == nil || result.Status != protocol.AgentMsgStatusDelivered {
			t.Fatalf("result = %+v, want delivered", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a message to a working target waited for a turn that cannot open")
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("pasted %d times, want 1: %q", len(prompts), prompts)
	}
}
