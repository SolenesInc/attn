package main_test

import (
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"strings"
	"testing"
)

func TestSessionPriorityCLI(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("priority"))
	s.Launched(id)
	for _, value := range []string{"on", "off"} {
		result := s.Run(testworld.Invocation{Session: id, Args: []string{"session", "priority", value}})
		if result.Code != 0 {
			t.Fatalf("priority %s: %s", value, result.Stderr)
		}
		testworld.AwaitSession(app, id, func(row protocol.Session) bool { return protocol.Deref(row.Priority) == (value == "on") })
	}
	delegatedResult := s.Run(testworld.Invocation{Session: id, Args: []string{"delegate", "--brief", "Priority task", "--cwd", s.Path("priority"), "--name", "priority worker", "--agent", "claude", "--model", "default", "--priority"}})
	if delegatedResult.Code != 0 {
		t.Fatalf("delegate --priority: %s", delegatedResult.Stderr)
	}
	var worker delegated
	delegatedResult.JSON(t, &worker)
	s.Launched(worker.SessionID).Prompted()
	testworld.AwaitSession(app, worker.SessionID, func(row protocol.Session) bool { return protocol.Deref(row.Priority) })
	for _, target := range []string{"missing-session", id} {
		if target == id {
			closed := testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: id}, protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == id })
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
