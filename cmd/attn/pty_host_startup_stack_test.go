package main_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSharedHostCanBeEnabledWhileGitCommandsAreRunning(t *testing.T) {
	t.Parallel()
	host := os.Getenv("ATTN_TEST_PTY_HOST")
	if host == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run the shared PTY host stack tests")
	}
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
	s.Start()
	git, settings := s.App(), s.App()
	cwd := s.Path("shop")
	if output, err := exec.Command("git", "init", "--initial-branch=main", cwd).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	const branchRequests = 8
	for range branchRequests {
		git.Send(protocol.GetDefaultBranchMessage{Cmd: protocol.CmdGetDefaultBranch, Repo: cwd})
	}
	enableSharedHost(t, settings)
	for range branchRequests {
		result := testworld.Await(git, protocol.EventGetDefaultBranchResult, func(protocol.GetDefaultBranchResultMessage) bool { return true })
		if !result.Success || result.Branch != "main" {
			t.Fatalf("default branch: %+v", result)
		}
	}
	shared := s.Spawn(settings, fakeagent.Harness(protocol.SessionAgentShell), cwd)
	settings.TypeLine(shared, "echo shared-$((6*7))")
	settings.AwaitScreen(shared, "shared-42")
}
