package daemon

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/crew"
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

	drains := observeAgentMailboxDrainsFor(t, d, woken.SessionID)
	if !d.applyState(sessionStateChange{
		sessionID: woken.SessionID,
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("idle state did not apply")
	}
	if delivered := drains.next(); delivered != 1 {
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

func TestCrewSleep_ADeadDayWithARestartPendingFailsTheRestart(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{RequestID: "sleep-instead", SessionID: woken.SessionID, State: crew.RestartQueued}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}
	runtime.running[woken.SessionID] = false

	resp := crewSleepCall(t, d, "alder")
	if !resp.Ok || !resp.CrewSleepResult.AlreadyAsleep {
		t.Fatalf("sleep = %+v / %v", resp.CrewSleepResult, protocol.Deref(resp.Error))
	}
	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateFailed {
		t.Fatalf("restart = %+v, want failed because the user chose sleep", member.Restart)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Fatalf("spawns = %d, want no wake", len(spawnedSessions(t, backend)))
	}
}

func TestCrewSleep_ALiveDayWithARestartPendingWithdrawsTheRestart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close protocol.CrewDayClose
	}{{name: "default"}, {name: "explicit nap", close: protocol.CrewDayCloseNap}} {
		t.Run(tc.name, func(t *testing.T) {
			d, backend, _ := newWakeableDaemon(t)
			woken, err := d.crewWake("trellis", "")
			if err != nil {
				t.Fatalf("wake: %v", err)
			}
			if _, err := setCrewRestart(d, "trellis", &crew.Restart{RequestID: "then-sleep", SessionID: woken.SessionID, State: crew.RestartRequested}); err != nil {
				t.Fatalf("seed pending restart: %v", err)
			}

			resp := crewSleepCall(t, d, "trellis")
			if !resp.Ok || resp.CrewSleepResult.AlreadyAsleep {
				t.Fatalf("sleep = %+v / %v", resp.CrewSleepResult, protocol.Deref(resp.Error))
			}
			withdrawn, _, err := d.crewMember("trellis")
			if err != nil || withdrawn.Restart == nil || withdrawn.Restart.State != crew.RestartFailed || !withdrawn.Restart.Withdrawn {
				t.Fatalf("restart after the sleep request = %+v, err=%v, want withdrawn", withdrawn.Restart, err)
			}

			msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID, Note: "Stale restart prompt.", Close: protocol.Ptr(tc.close)}
			handoff := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
			if !handoff.Ok || protocol.Deref(handoff.CrewHandoffResult.Outcome) != protocol.CrewDayCloseSleep || handoff.CrewHandoffResult.SessionID != nil {
				t.Fatalf("handoff = %+v / %v", handoff.CrewHandoffResult, protocol.Deref(handoff.Error))
			}
			after, _, err := d.crewMember("trellis")
			if err != nil || after.Restart == nil || after.Restart.State != withdrawn.Restart.State || !after.Restart.Withdrawn || after.Restart.RequestID != withdrawn.Restart.RequestID || after.Restart.Error != withdrawn.Restart.Error {
				t.Fatalf("stale handoff rewrote the withdrawn restart: %+v, err=%v, want %+v", after.Restart, err, withdrawn.Restart)
			}
			if after.BindingSession != "" || len(spawnedSessions(t, backend)) != 1 {
				t.Fatalf("stale handoff binding/spawns = %q/%d, want asleep with one spawn", after.BindingSession, len(spawnedSessions(t, backend)))
			}
			d.completeCrewRestart("trellis", "then-sleep", woken.SessionID, "ignored", "ignored")
			if err := d.failCrewRestart("trellis", "then-sleep", woken.SessionID, "ignored", errors.New("ignored")); err != nil {
				t.Fatalf("stale failure: %v", err)
			}
			settled, _, err := d.crewMember("trellis")
			if err != nil || settled.Restart == nil || !settled.Restart.Withdrawn || settled.Restart.Error != withdrawn.Restart.Error {
				t.Fatalf("stale settlement rewrote withdrawal: %+v, err=%v", settled.Restart, err)
			}
			fresh := crewRestartCall(t, d, "trellis", "restart-after-sleep")
			if !fresh.Ok || fresh.CrewRestartResult.Restart.State != protocol.CrewRestartStateCompleted {
				t.Fatalf("fresh restart after withdrawal = %+v / %v", fresh.CrewRestartResult, protocol.Deref(fresh.Error))
			}
			current, _, err := d.crewMember("trellis")
			if err != nil || current.Restart == nil || current.Restart.Withdrawn || current.Restart.RequestID != "restart-after-sleep" {
				t.Fatalf("fresh restart retained withdrawal: %+v, err=%v", current.Restart, err)
			}
		})
	}
}
