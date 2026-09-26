package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentPeekShowsStateTodosWorkspaceLatestReplyAndScreen(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "plan the discount field")
	codex.Prompted()
	codex.Reply("first answer <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(session, "build it")
	codex.Prompted()
	todos := []string{"[✓] read the plan", "[→] build peek"}
	if err := cli.UpdateTodos(session, todos); err != nil {
		t.Fatal(err)
	}
	codex.Reply("latest answer <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	peek, err := cli.AgentPeek(session)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if peek.SessionID != session || peek.State != string(protocol.SessionStateWaitingInput) || peek.WorkspaceID != "workspace-shop" {
		t.Errorf("peek = %+v, want %s waiting for input in workspace-shop", peek, session)
	}
	if strings.Join(peek.Todos, "|") != strings.Join(todos, "|") {
		t.Errorf("todos = %q, want %q", peek.Todos, todos)
	}
	if last := protocol.Deref(peek.LastAssistantMessage); !strings.Contains(last, "latest answer") || strings.Contains(last, "first answer") {
		t.Errorf("last assistant message = %q, want the latest reply", last)
	}
	if peek.Screen == nil || !strings.Contains(peek.Screen.Text, "latest answer") || peek.Screen.Cols != 100 || peek.Screen.Rows != 30 {
		t.Errorf("screen = %+v, want the 100x30 screen showing the latest reply", peek.Screen)
	}
}

func TestAgentPeekResolvesItsAddress(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := w.Spawn(app, fakeagent.Claude, w.Path("first"), withIDPrefix("aaa-"))
	for _, session := range []string{
		first,
		w.Spawn(app, fakeagent.Claude, w.Path("second"), withIDPrefix("aab-")),
		w.Spawn(app, fakeagent.Claude, w.Path("keel-prefix"), withIDPrefix("keel-")),
	} {
		w.Launched(session)
	}
	firstDay := wakeCrew(t, cli, "keel", "")
	firstDayAgent := w.Launched(firstDay.SessionID)
	peekResolves := func(address, want string) {
		t.Helper()
		peek, err := cli.AgentPeek(address)
		if err != nil || peek.SessionID != want {
			t.Errorf("peek %q = %+v, %v; want %s", address, peek, err, want)
		}
	}
	peekRefuses := func(address, want string) {
		t.Helper()
		if peek, err := cli.AgentPeek(address); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("peek %q = %+v, %v; want a refusal naming %s", address, peek, err, want)
		}
	}

	peekResolves("aaa", first)
	peekRefuses("aa", "ambiguous_session")
	peekRefuses("zzz", "session_not_found")
	peekResolves("Keel", firstDay.SessionID)

	firstDayAgent.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == firstDay.SessionID })
	nextDay := wakeCrew(t, cli, "keel", "")
	w.Launched(nextDay.SessionID)
	peekResolves("keel", nextDay.SessionID)
	w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("keel-session"), func(m *protocol.SpawnSessionMessage) { m.ID = "keel" }))
	peekResolves("keel", "keel")

	sessions := crewSessionCount(t, cli)
	peekRefuses("trellis", "crew_member_asleep")
	if got := crewSessionCount(t, cli); got != sessions {
		t.Errorf("peeking the sleeping trellis changed the sessions from %d to %d", sessions, got)
	}
	if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
		t.Errorf("peeking woke trellis into %s", *binding)
	}
}

func TestAgentPeekForgetsAnExitOnceARespawnSucceeds(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("deploy")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	claude := w.Launched(session)
	app.TypeLine(session, "deploy it")
	claude.Prompted()
	claude.Reply("Error: Model \"gpt-5.6-sol\" is ambiguous across providers <!-- attn:state=idle -->")
	claude.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	exited, _ := peekExit(t, cli, session, "is ambiguous across providers")
	if exited.Code != 1 || exited.Signal != nil || exited.At == "" {
		t.Errorf("exit = %+v, want code 1 with its time", exited)
	}

	w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	w.Launched(session)
	if peek, err := cli.AgentPeek(session); err != nil || peek.Exit != nil {
		t.Errorf("peek after a successful respawn = %+v, %v; want the exit forgotten", peek, err)
	}
}

func TestAnOversizedExitScreenKeepsItsTailAndSaysSo(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	shell := w.Spawn(app, fakeagent.Harness(protocol.AgentShellValue), w.Path("wide"), func(m *protocol.SpawnSessionMessage) {
		m.Cols, m.Rows = 1000, 300
	})
	app.TypeLine(shell, `awk 'BEGIN { for (i = 0; i < 300; i++) { printf "%04d", i; for (j = 0; j < 990; j++) printf "x"; print "" } }'; exit 3`)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == shell })

	peek, err := cli.AgentPeek(shell)
	if err != nil || peek.Exit == nil || peek.Exit.Code != 3 || peek.Screen == nil {
		t.Fatalf("peek of the exited shell = %+v, %v; want exit 3 with its screen", peek, err)
	}
	text := peek.Screen.Text
	head, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(head, "[exit screen truncated: ") || !strings.Contains(head, "attn keeps the last 262144]") {
		t.Errorf("the kept screen opens with %q, want a notice naming the cap", head)
	}
	if len(text) > 262144+len(head)+1 {
		t.Errorf("the kept screen is %d bytes, want at most the cap and its notice", len(text))
	}
	if !strings.Contains(text, "0299"+strings.Repeat("x", 990)) {
		t.Errorf("the kept screen lost its last row")
	}
	if strings.Contains(text, "0010"+strings.Repeat("x", 990)) {
		t.Errorf("the kept screen still holds a row from the head the cap drops")
	}
}

func withIDPrefix(prefix string) func(*protocol.SpawnSessionMessage) {
	return func(m *protocol.SpawnSessionMessage) { m.ID = prefix + m.ID }
}
