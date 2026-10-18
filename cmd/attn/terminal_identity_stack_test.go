package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACommandCarryingItsTerminalsIDSpeaksAsTheSessionTheTerminalShows(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	const desk = "desk-3333-4444"
	register(t, s, desk, "desk")
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(session)
	carried, ok := envValue(claude.Env, "ATTN_TERMINAL_ID")
	if !ok || carried != app.Terminal(session) {
		t.Fatalf("claude carries ATTN_TERMINAL_ID=%q, want the id of the terminal its pane places", carried)
	}
	fromTheAgent := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Terminal: carried})
	}

	if carried == session {
		t.Fatalf("terminal %s must differ from session %s", carried, session)
	}
	if got := fromTheAgent("presence"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "running inside attn (session "+session+")" {
		t.Fatalf("presence: %+v", got)
	}
	if got := s.Run(testworld.Invocation{Args: []string{"presence"}, Env: []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=" + carried}}); got.Code != 0 || strings.TrimSpace(got.Stdout) != "running inside attn (session "+session+")" {
		t.Fatalf("legacy terminal spelling: %+v", got)
	}
	if got := fromTheAgent("session", "rename", "checkout"); got.Code != 0 || strings.TrimSpace(got.Stdout) != session+` renamed to "checkout"` {
		t.Fatalf("rename exited %d: %s", got.Code, got.Stderr)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.Label == "checkout" })
	var moved protocol.DesktopMoveSessionResult
	fromTheAgent("session", "move", "1", "--json").JSON(t, &moved)
	if string(moved.SessionID) != session {
		t.Errorf("session move from the agent moved %+v, want its own session %s", moved, session)
	}
	if listed := fromTheAgent("pr", "ls"); listed.Code != 0 || listed.Stdout != "session "+session+" has opened no pull requests\n" {
		t.Errorf("pr ls exited %d and printed %q, want the session's own empty list", listed.Code, listed.Stdout)
	}

	var sent protocol.AgentMsgResult
	fromTheAgent("agent", "msg", desk, "the build is green", "--json").JSON(t, &sent)
	var batch protocol.AgentInboxBatchResult
	s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: desk}).JSON(t, &batch)
	if len(batch.Items) != 1 || string(protocol.Deref(batch.Items[0].Sender).Ref) != "session:"+session {
		t.Fatalf("desk's inbox holds %+v, want the message sent by session %s", batch.Items, session)
	}

	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	answer := s.Run(testworld.Invocation{Args: []string{"agent", "msg", session, "thanks, merging"}, Session: desk})
	_, messageID, _ := strings.Cut(strings.TrimSpace(answer.Stdout), " (id ")
	messageID = strings.TrimSuffix(messageID, ")")
	if answer.Code != 0 || messageID == "" {
		t.Fatalf("desk's answer exited %d and printed %q", answer.Code, answer.Stdout)
	}
	claude.Prompted()
	read := fromTheAgent("agent", "inbox", messageID)
	if read.Code != 0 {
		t.Fatalf("reading the answer from the terminal exited %d: %s", read.Code, read.Stderr)
	}
	requireLines(t, "the agent's read", read.Stdout, "thanks, merging")
}

func TestCallerReceiptsFollowAClearInTheSameTerminal(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	terminalID := app.Terminal(first)
	claude := s.Launched(first)
	app.TypeLine(first, "/clear")
	if got := claude.Prompted(); got != "/clear" {
		t.Fatalf("prompt %q", got)
	}
	next := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(protocol.Deref(e.Session.Succeeds)) == first
	}).Session
	if app.Terminal(string(next.ID)) != terminalID {
		t.Fatal("clear moved the terminal")
	}
	fromAgent := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Args: args, Terminal: terminalID})
	}
	if got := fromAgent("presence"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "running inside attn (session "+string(next.ID)+")" {
		t.Fatalf("presence after clear: %+v", got)
	}
	if got := fromAgent("session", "rename", "after clear"); got.Code != 0 || strings.TrimSpace(got.Stdout) != string(next.ID)+` renamed to "after clear"` {
		t.Fatalf("rename after clear: %+v", got)
	}

	for _, mode := range []string{"on", "off"} {
		if got := fromAgent("session", "priority", mode); got.Code != 0 || strings.TrimSpace(got.Stdout) != string(next.ID)+" priority "+mode {
			t.Fatalf("priority after clear: %+v", got)
		}
		testworld.AwaitSession(app, string(next.ID), func(session protocol.Session) bool { return protocol.Deref(session.Priority) == (mode == "on") })
	}
	if got := fromAgent("pr", "ls"); got.Code != 0 || got.Stdout != "session "+string(next.ID)+" has opened no pull requests\n" {
		t.Fatalf("PR receipt after clear: %+v", got)
	}
}
