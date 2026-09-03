package daemon

import (
	"bytes"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func crewSleepCall(t *testing.T, d *Daemon, member string) protocol.Response {
	t.Helper()
	return gardenCall(t, func(c net.Conn) {
		d.handleCrewSleep(c, &protocol.CrewSleepMessage{Cmd: protocol.CmdCrewSleep, Member: member})
	})
}

func TestCrewSleep_DeliversAUserRequestForSleep(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if !d.applyState(sessionStateChange{
		sessionID: woken.SessionID,
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("idle state did not apply")
	}
	var mu sync.Mutex
	var typed bytes.Buffer
	backend.onInput = func(id string, data []byte) {
		if id != woken.SessionID {
			t.Errorf("typed into %q, want %q", id, woken.SessionID)
		}
		mu.Lock()
		defer mu.Unlock()
		typed.Write(data)
	}

	resp := crewSleepCall(t, d, "trellis")
	if !resp.Ok {
		t.Fatalf("crew sleep: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewSleepResult
	if result == nil || result.AlreadyAsleep || protocol.Deref(result.DeliveryStatus) != protocol.AgentMsgStatusNotified {
		t.Fatalf("sleep result = %+v, want delivered request", result)
	}
	mu.Lock()
	text := typed.String()
	mu.Unlock()
	if !strings.Contains(text, agentMailboxDoorbellText) {
		t.Fatalf("composer input = %q, want generic inbox doorbell", text)
	}
	if strings.Contains(text, "The user is asking you") {
		t.Fatalf("sleep request body leaked into the terminal: %q", text)
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries(woken.SessionID); err != nil || len(unread) != 1 || unread[0].Item.Prompt != crewRequestedSleepPrompt || unread[0].Item.NotifiedAt == "" {
		t.Fatalf("unread inbox = %v, %v; request must stay durable after the doorbell", unread, err)
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("sleep request killed the member")
	}
}

func TestCrewSleep_AlreadyAsleepIsANamedNoOp(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	resp := crewSleepCall(t, d, "alder")
	if !resp.Ok {
		t.Fatalf("crew sleep: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewSleepResult
	if result == nil || !result.AlreadyAsleep || !strings.Contains(result.Detail, "already asleep") || !strings.Contains(result.Detail, "no sleep request was sent") {
		t.Fatalf("sleep result = %+v, want named no-op", result)
	}
}

func TestCrewSleep_QueuesWhileWakingAndWakesOnIdleWithoutPromptHook(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if !d.initialPromptPending(woken.SessionID) {
		t.Fatal("ordinary wake did not gate messages behind its first prompt")
	}

	var mu sync.Mutex
	var typed bytes.Buffer
	backend.onInput = func(id string, data []byte) {
		if id != woken.SessionID {
			t.Errorf("typed into %q, want %q", id, woken.SessionID)
		}
		mu.Lock()
		defer mu.Unlock()
		typed.Write(data)
	}

	result, err := d.crewSleep("trellis")
	if err != nil {
		t.Fatalf("sleep: %v", err)
	}
	if protocol.Deref(result.DeliveryStatus) != protocol.AgentMsgStatusQueued ||
		!strings.Contains(result.Detail, "not taking input") {
		t.Fatalf("sleep result = %+v, want queued behind first prompt", result)
	}
	mu.Lock()
	beforePrompt := typed.String()
	mu.Unlock()
	if beforePrompt != "" {
		t.Fatalf("sleep request reached startup before the first prompt: %q", beforePrompt)
	}

	drained := make(chan int, 1)
	d.agentMailboxDrainHook = func(sessionID string, delivered int) {
		if sessionID == woken.SessionID {
			drained <- delivered
		}
	}
	if !d.applyState(sessionStateChange{
		sessionID: woken.SessionID,
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("idle state did not apply")
	}
	if delivered := <-drained; delivered != 1 {
		t.Fatalf("idle drain delivered %d messages, want 1", delivered)
	}
	mu.Lock()
	afterPrompt := typed.String()
	mu.Unlock()
	if !strings.Contains(afterPrompt, agentMailboxDoorbellText) || strings.Contains(afterPrompt, "attn handoff --sleep") {
		t.Fatalf("generic inbox doorbell did not land after the greeting: %q", afterPrompt)
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries(woken.SessionID); err != nil || len(unread) != 1 || unread[0].Item.NotifiedAt == "" {
		t.Fatalf("unread inbox after first prompt = %v, %v", unread, err)
	}
}
