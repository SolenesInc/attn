package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/protocol"
)

func addHostSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Agent:          "nisse",
		Label:          id,
		Directory:      t.TempDir(),
		State:          protocol.StateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun(id, "attn-pi", "run-"+id) {
		t.Fatalf("failed to begin the driver run for %s", id)
	}
}

func declare(d *Daemon, id string, seq int, kind, state string) {
	d.handleHostEvent(hostsession.Event{
		SessionID:   id,
		Seq:         seq,
		Kind:        kind,
		Body:        map[string]interface{}{"state": state},
		LifecycleID: "run-" + id,
	})
}

func stateOf(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.store.Get(id)
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return string(session.State)
}

func TestHostDeclarationsMoveTheSessionAndOpenItsTurn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-1")

	declare(d, "conv-1", 1, "session_ready", protocol.StateIdle)
	if got := stateOf(t, d, "conv-1"); got != protocol.StateIdle {
		t.Fatalf("state after session_ready = %q, want idle: a session nobody has spoken to is at its prompt", got)
	}
	if !owed(t, d, "conv-1") {
		t.Fatal("a conversation session sitting at its prompt owes no turn")
	}

	initialRequestAt := protocol.Deref(d.store.Get("conv-1").LastModelRequestAt)
	declare(d, "conv-1", 2, "run_started", protocol.StateWorking)
	if got := stateOf(t, d, "conv-1"); got != protocol.StateWorking {
		t.Fatalf("state after run_started = %q, want working", got)
	}
	if !owed(t, d, "conv-1") {
		t.Fatal("prompting the agent settled its turn; only the user settles")
	}
	if requestAt := protocol.Deref(d.store.Get("conv-1").LastModelRequestAt); requestAt == "" || requestAt == initialRequestAt {
		t.Fatalf("run_started left model-request clock at %q", requestAt)
	}

	declare(d, "conv-1", 3, "run_settled", protocol.StateIdle)
	if got := stateOf(t, d, "conv-1"); got != protocol.StateIdle {
		t.Fatalf("state after run_settled = %q, want idle", got)
	}
}

func TestHostDeclarationOutOfOrderIsDiscarded(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-2")

	declare(d, "conv-2", 5, "run_started", protocol.StateWorking)
	declare(d, "conv-2", 4, "run_settled", protocol.StateIdle)

	if got := stateOf(t, d, "conv-2"); got != protocol.StateWorking {
		t.Fatalf("state = %q, want working: seq 4 arrived after seq 5 and must not win", got)
	}
}

func TestHostDeclarationFromASupersededRunIsDiscarded(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-3")

	d.handleHostEvent(hostsession.Event{
		SessionID:   "conv-3",
		Seq:         1,
		Kind:        "run_started",
		Body:        map[string]interface{}{"state": protocol.StateWorking},
		LifecycleID: "run-from-a-previous-life",
	})

	if got := stateOf(t, d, "conv-3"); got != protocol.StateLaunching {
		t.Fatalf("state = %q, want launching: a stale host's run id owns nothing", got)
	}
}

func TestHostDeclarationDuringLaunchIsAppliedAtCommit(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "conv-4",
		Agent:          "nisse",
		Label:          "conv-4",
		Directory:      t.TempDir(),
		State:          protocol.StateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.beginPluginSessionLaunch("conv-4", "attn-pi", "run-conv-4")

	declare(d, "conv-4", 1, "session_ready", protocol.StateIdle)
	if got := stateOf(t, d, "conv-4"); got != protocol.StateLaunching {
		t.Fatalf("state during launch = %q, want launching: there is no run cursor to report against yet", got)
	}

	if !d.store.BeginAgentDriverRun("conv-4", "attn-pi", "run-conv-4") {
		t.Fatal("failed to begin the driver run")
	}
	d.finishPluginSessionLaunch("conv-4", true)

	if got := stateOf(t, d, "conv-4"); got != protocol.StateIdle {
		t.Fatalf("state after commit = %q, want idle: the queued declaration must land", got)
	}
}

func TestHostRenderingsDoNotMoveTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-5")
	declare(d, "conv-5", 1, "session_ready", protocol.StateIdle)

	d.handleHostEvent(hostsession.Event{
		SessionID:   "conv-5",
		Seq:         2,
		Kind:        "queue_update",
		Body:        map[string]interface{}{"state": protocol.StateWorking, "steering": []interface{}{"hi"}},
		LifecycleID: "run-conv-5",
	})

	if got := stateOf(t, d, "conv-5"); got != protocol.StateIdle {
		t.Fatalf("state = %q, want idle: a render kind carries no authority over session state", got)
	}
}

func TestHostToolEventsDoNotRestampTheState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-7")
	declare(d, "conv-7", 1, "session_ready", protocol.StateIdle)
	declare(d, "conv-7", 2, "run_started", protocol.StateWorking)

	before := d.store.Get("conv-7").StateSince
	d.handleHostEvent(hostsession.Event{
		SessionID:   "conv-7",
		Seq:         3,
		Kind:        "tool_started",
		Body:        map[string]interface{}{"call_id": "c1", "name": "bash", "state": protocol.StateIdle},
		LifecycleID: "run-conv-7",
	})
	d.handleHostEvent(hostsession.Event{
		SessionID:   "conv-7",
		Seq:         4,
		Kind:        "tool_finished",
		Body:        map[string]interface{}{"call_id": "c1", "name": "bash", "status": "ok"},
		LifecycleID: "run-conv-7",
	})

	session := d.store.Get("conv-7")
	if got := string(session.State); got != protocol.StateWorking {
		t.Fatalf("state = %q, want working: a tool boundary says nothing about the session's state", got)
	}
	if session.StateSince != before {
		t.Fatalf("state_since moved from %q to %q: a tool boundary must not restart the working clock", before, session.StateSince)
	}
}

func TestToolDetailAndClearQueueReachTheHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-8")

	echo := filepath.Join(t.TempDir(), "echo-host.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line; do\n" +
		"  escaped=$(printf '%s' \"$line\" | sed 's/\"/\\\\\"/g')\n" +
		"  printf '{\"session_id\":\"conv-8\",\"seq\":1,\"kind\":\"message_end\",\"body\":{\"verb\":\"%s\"}}\\n' \"$escaped\" >&3\n" +
		"done\n"
	if err := os.WriteFile(echo, []byte(script), 0o755); err != nil {
		t.Fatalf("write echo host: %v", err)
	}

	received := make(chan string, 4)
	manager := hostsession.New(d.logf, func(event hostsession.Event) {
		if verb, ok := event.Body["verb"].(string); ok {
			received <- verb
		}
	}, func(hostsession.ExitInfo) {})
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: "conv-8", Command: []string{echo}}); err != nil {
		t.Fatalf("spawn echo host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill("conv-8") })

	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleAgentToolDetail(client, &protocol.AgentToolDetailMessage{
		Cmd:    protocol.CmdAgentToolDetail,
		ID:     "conv-8",
		CallID: "call-42",
		Full:   protocol.Ptr(true),
	})
	d.handleAgentClearQueue(client, &protocol.AgentClearQueueMessage{Cmd: protocol.CmdAgentClearQueue, ID: "conv-8"})

	want := []string{`"verb":"tool_detail"`, `"verb":"clear_queue"`}
	got := make([]string, 0, 2)
	for range want {
		select {
		case verb := <-received:
			got = append(got, verb)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d verbs reached the host: %v", len(got), len(want), got)
		}
	}
	joined := strings.Join(got, "\n")
	for _, fragment := range append(want, `"call_id":"call-42"`, `"full":true`) {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("host received %q, want it to contain %s", joined, fragment)
		}
	}
}

func TestToolDetailWithoutAHostIsAnError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 10)}

	d.handleAgentToolDetail(client, &protocol.AgentToolDetailMessage{
		Cmd:    protocol.CmdAgentToolDetail,
		ID:     "gone",
		CallID: "call-1",
	})

	select {
	case msg := <-client.send:
		if !strings.Contains(string(msg.payload), "no live conversation host") {
			t.Fatalf("client got %q, want an error naming the missing host", string(msg.payload))
		}
	default:
		t.Fatal("no command error for a detail fetch against a session with no host")
	}
}

func TestTypeDoorbellSteersAConversationSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var ptyInput bool
	backend.onInput = func(string, []byte) { ptyInput = true }
	d.ptyBackend = backend
	addHostSession(t, d, "conv-6")

	echo := filepath.Join(t.TempDir(), "echo-host.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line; do\n" +
		"  escaped=$(printf '%s' \"$line\" | sed 's/\"/\\\\\"/g')\n" +
		"  printf '{\"session_id\":\"conv-6\",\"seq\":1,\"kind\":\"message_end\",\"body\":{\"verb\":\"%s\"}}\\n' \"$escaped\" >&3\n" +
		"done\n"
	if err := os.WriteFile(echo, []byte(script), 0o755); err != nil {
		t.Fatalf("write echo host: %v", err)
	}

	received := make(chan string, 4)
	manager := hostsession.New(d.logf, func(event hostsession.Event) {
		if verb, ok := event.Body["verb"].(string); ok {
			received <- verb
		}
	}, func(hostsession.ExitInfo) {})
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: "conv-6", Command: []string{echo}}); err != nil {
		t.Fatalf("spawn echo host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill("conv-6") })

	delivery := maintenanceSessionInput("host-test", "conv-6", "conv-6", "a ticket needs you", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("session input error = %v, want nil", attempt.err)
	}

	select {
	case verb := <-received:
		if !strings.Contains(verb, `"verb":"steer"`) {
			t.Fatalf("host received %q, want a steer verb", verb)
		}
		if !strings.Contains(verb, "a ticket needs you") {
			t.Fatalf("host received %q, want the doorbell text", verb)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the doorbell never reached the host")
	}
	if ptyInput {
		t.Fatal("session input typed into a PTY for a conversation session")
	}
}
