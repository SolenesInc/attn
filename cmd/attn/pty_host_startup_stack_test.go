package main_test

import (
	"os/exec"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSharedHostCanBeEnabledWhileGitCommandsAreRunning(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.UseSharedHostBinary(testworld.RequireSharedHost(t))
	s.Start()
	git, settings := s.App(), s.App()
	cwd := s.Path("shop")
	if output, err := exec.Command("git", "init", "--initial-branch=main", cwd).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	const branchRequests = 8
	for range branchRequests {
		git.Send(protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: cwd})
	}
	s.EnableSharedHost(settings)
	for range branchRequests {
		result := testworld.Await(git, protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
		if !result.Success || result.Info == nil || result.Info.CurrentBranch != "main" {
			t.Fatalf("repo info: %+v", result)
		}
	}
	shared := s.Spawn(settings, fakeagent.Harness(protocol.SessionAgentShell), cwd)
	settings.TypeLine(shared, "echo shared-$((6*7))")
	settings.AwaitScreen(shared, "shared-42")
}
