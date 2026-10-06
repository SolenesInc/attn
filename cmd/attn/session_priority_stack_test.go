package main_test

import (
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"os"
	"strings"
	"testing"
)

func TestDelegateRetryWithoutPriorityKeepsTheAcceptedRequest(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	cwd := s.Path("retry")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "before-priority", ProfileID: protocol.Ptr(app.SelectedProfile()),
		Cwd: cwd, Agent: protocol.Ptr("claude"), Model: protocol.Ptr(""),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Run once")},
	}
	accepted, err := s.Client().Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	s.Launched(string(accepted.SessionID)).Prompted()
	s.Stop()
	s.Start()
	retry := s.Attn("delegate", "--request-id", request.RequestID, "--profile", app.SelectedProfile(),
		"--cwd", cwd, "--agent", "claude", "--model", "default", "--brief", "Run once")
	if retry.Code != 0 {
		t.Fatalf("retrying the request without priority: %s", retry.Stderr)
	}
	var result delegated
	retry.JSON(t, &result)
	if result.SessionID != string(accepted.SessionID) || result.SeedID != accepted.SeedID {
		t.Fatalf("retry = %+v, want session %s and seed %s", result, accepted.SessionID, accepted.SeedID)
	}
	if sessions, err := s.Client().Query(""); err != nil || len(sessions) != 1 {
		t.Fatalf("sessions after retry = %+v, %v; want one", sessions, err)
	}
}

func TestSessionPriorityCLI(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("priority"))
	cliAgent := s.Launched(id)
	terminal, ok := envValue(cliAgent.Env, "ATTN_TERMINAL_ID")
	if !ok || terminal != app.Terminal(id) {
		t.Fatalf("agent carries ATTN_TERMINAL_ID=%q, want its terminal", terminal)
	}
	for _, value := range []string{"on", "off"} {
		result := s.Run(testworld.Invocation{Terminal: terminal, Args: []string{"session", "priority", value}})
		if result.Code != 0 {
			t.Fatalf("priority %s: %s", value, result.Stderr)
		}
		testworld.AwaitSession(app, id, func(row protocol.Session) bool { return protocol.Deref(row.Priority) == (value == "on") })
	}
	delegatedResult := s.Run(testworld.Invocation{Terminal: terminal, Args: []string{"delegate", "--brief", "Priority task", "--cwd", s.Path("priority"), "--name", "priority worker", "--agent", "claude", "--model", "default", "--priority"}})
	if delegatedResult.Code != 0 {
		t.Fatalf("delegate --priority: %s", delegatedResult.Stderr)
	}
	var worker delegated
	delegatedResult.JSON(t, &worker)
	s.Launched(worker.SessionID).Prompted()
	testworld.AwaitSession(app, worker.SessionID, func(row protocol.Session) bool { return protocol.Deref(row.Priority) })
	for _, target := range []string{"missing-session", id} {
		if target == id {
			closed := testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)}, protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == protocol.SessionID(id) })
			if !closed.Accepted {
				t.Fatal("closing session refused")
			}
		}
		result := s.Attn("session", "priority", "on", "--session", target)
		if result.Code != 1 || !strings.Contains(result.Stderr, target) {
			t.Fatalf("priority on %s: exit=%d stderr=%q", target, result.Code, result.Stderr)
		}
	}
}
