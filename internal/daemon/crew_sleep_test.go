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
	d.runPostInitialPrompt(woken.SessionID, protocol.StateWorking)
	oldWindow := sessionInputTakenWindow
	sessionInputTakenWindow = 0
	t.Cleanup(func() { sessionInputTakenWindow = oldWindow })

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
	if result == nil || result.AlreadyAsleep || protocol.Deref(result.DeliveryStatus) != protocol.AgentMsgStatusDelivered {
		t.Fatalf("sleep result = %+v, want delivered request", result)
	}
	mu.Lock()
	text := typed.String()
	mu.Unlock()
	for _, want := range []string{"The user is asking you", "attn handoff --sleep", "nobody wakes behind you"} {
		if !strings.Contains(text, want) {
			t.Errorf("composer input is missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "another agent, not from your user") {
		t.Fatalf("user request masqueraded as an agent message: %q", text)
	}
	if queued, err := d.store.QueuedAgentMailboxDeliveries(woken.SessionID); err != nil || len(queued) != 0 {
		t.Fatalf("queued messages = %v, %v; delivered request must be stamped", queued, err)
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

func TestCrewSleep_QueuesUntilAWakingMemberTakesItsFirstPrompt(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if !d.initialPromptPending(woken.SessionID) {
		t.Fatal("ordinary wake did not gate messages behind its first prompt")
	}

	oldWindow := sessionInputTakenWindow
	sessionInputTakenWindow = 0
	t.Cleanup(func() { sessionInputTakenWindow = oldWindow })
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
		!strings.Contains(result.Detail, "still reading its priming") {
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
	d.runPostInitialPrompt(woken.SessionID, protocol.StateWorking)
	if delivered := <-drained; delivered != 1 {
		t.Fatalf("first-prompt drain delivered %d messages, want 1", delivered)
	}
	mu.Lock()
	afterPrompt := typed.String()
	mu.Unlock()
	if !strings.Contains(afterPrompt, "attn handoff --sleep") {
		t.Fatalf("sleep request did not land after the greeting: %q", afterPrompt)
	}
	if queued, err := d.store.QueuedAgentMailboxDeliveries(woken.SessionID); err != nil || len(queued) != 0 {
		t.Fatalf("queued messages after first prompt = %v, %v", queued, err)
	}
}
