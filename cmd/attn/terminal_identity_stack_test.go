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
	carried, ok := envValue(claude.Env, "ATTN_SESSION_ID")
	if !ok || carried != app.Terminal(session) {
		t.Fatalf("claude carries ATTN_SESSION_ID=%q, want the id of the terminal its pane places", carried)
	}
	fromTheAgent := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: carried})
	}

	if got := fromTheAgent("session", "rename", "checkout"); got.Code != 0 {
		t.Fatalf("rename exited %d: %s", got.Code, got.Stderr)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.Label == "checkout" })
	if listed := fromTheAgent("pr", "ls"); listed.Code != 0 || listed.Stdout != "session "+session+" has opened no pull requests\n" {
		t.Errorf("pr ls exited %d and printed %q, want the session's own empty list", listed.Code, listed.Stdout)
	}

	var sent protocol.AgentMsgResult
	fromTheAgent("agent", "msg", desk, "the build is green", "--json").JSON(t, &sent)
	var batch protocol.AgentInboxBatchResult
	s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: desk}).JSON(t, &batch)
	if len(batch.Items) != 1 || protocol.Deref(batch.Items[0].SenderSessionID) != session {
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
