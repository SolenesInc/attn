package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryHookCommandASessionHoldsStaysAccepted(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(id)
	terminal := app.Terminal(id)

	hook := func(stdin string, args ...string) testworld.Result {
		t.Helper()
		result := s.Run(testworld.Invocation{Args: args, Stdin: stdin, Terminal: terminal})
		if result.Code != 0 || result.Stderr != "" {
			t.Fatalf("attn %q: exit %d, stderr %q", args, result.Code, result.Stderr)
		}
		return result
	}

	transcript := s.Path("transcript.jsonl")
	hook(`{"session_id":"`+terminal+`","transcript_path":"`+transcript+`"}`, "_hook-session-start", terminal)
	hook(`{"session_id":"`+terminal+`","transcript_path":"`+transcript+`","prompt":"add it"}`, "_hook-state", terminal, "working", "user_prompt_submit")
	testworld.AwaitSession(app, id, func(session protocol.Session) bool { return session.State == protocol.SessionStateWorking })
	hook(`{"permission_mode":"default"}`, "_hook-state", terminal, "pending_approval")
	testworld.AwaitSession(app, id, func(session protocol.Session) bool {
		return session.State == protocol.SessionStatePendingApproval
	})
	hook(`{"tool_name":"Bash","tool_input":{"command":"true"},"tool_response":"ok"}`, "_hook-tool-use", terminal)
	testworld.AwaitSession(app, id, func(session protocol.Session) bool { return session.State == protocol.SessionStateWorking })
	hook(`{}`, "_hook-state", "working")
	hook(`{"notification_type":"idle_prompt","message":"waiting"}`, "_hook-notification", terminal)
	hook(`{"error_type":"rate_limit","error_message":"slow down"}`, "_hook-stop-failure", terminal)
	hook(`{"trigger":"auto"}`, "_hook-compact", terminal, "start")
	hook(`{"trigger":"auto"}`, "_hook-compact", terminal, "end")
	hook(`{"background_tasks":[],"session_crons":[]}`, "_hook-stop", terminal)
	hook(`{"todos":[]}`, "_hook-todo", terminal)
}

func TestHookCommandsNameAWrongInstanceAndAMissingTerminal(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	invalid := s.Run(testworld.Invocation{Args: []string{"_hook-tool-use", "t1"}, Stdin: "{}", Env: []string{"ATTN_INSTANCE=Bad_Name!"}})
	if invalid.Code != 1 || !strings.Contains(invalid.Stderr, `invalid ATTN_INSTANCE="Bad_Name!"`) {
		t.Fatalf("invalid instance: exit %d, stderr %q", invalid.Code, invalid.Stderr)
	}

	usage := s.Run(testworld.Invocation{Args: []string{"_hook-tool-use"}, Stdin: "{}"})
	if usage.Code != 1 || !strings.Contains(usage.Stderr, "usage: attn _hook-tool-use [session_id]") {
		t.Fatalf("missing terminal: exit %d, stderr %q", usage.Code, usage.Stderr)
	}

	down := s.Run(testworld.Invocation{Args: []string{"_hook-state", "t1", "working"}, Stdin: "{}"})
	if down.Code != 1 || !strings.Contains(down.Stderr, "error updating state: connect to daemon at") {
		t.Fatalf("no daemon: exit %d, stderr %q", down.Code, down.Stderr)
	}
}
