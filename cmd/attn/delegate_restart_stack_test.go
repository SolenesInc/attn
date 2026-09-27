package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegateWhoseAgentOutlivesARestartStillFailsWhenItExitsBeforeItsFirstTurn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "outlives-restart", Cwd: cwd,
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("outlives-restart"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Say hello")},
	}
	boot := s.HoldNextBoot()
	if _, err := s.Client().StartDelegation(request); err != nil {
		t.Fatal(err)
	}
	s.AwaitHeldBoot()

	s.Stop()
	s.Start()
	const screen = `Error: Model "gpt-5.6-sol" is ambiguous across providers`
	s.ExitAtNextBoot(1, screen)
	boot()
	result, err := s.Client().Delegate(request)
	for _, want := range []string{"codex exited with code 1 before its first turn", "is ambiguous across providers"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("after the restart the delegation = %+v, %v; want the failure to say %q", result, err, want)
		}
	}
}
