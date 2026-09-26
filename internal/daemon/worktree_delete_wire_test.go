package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDeletingADirtyWorktreeNeedsForceAndThenTakesItsSessionsAlong(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	path := createWorktree(t, app, repo, "feat-dirty")
	session := w.Spawn(app, fakeagent.Codex, path)
	if err := os.WriteFile(filepath.Join(path, "local.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deleteWorktree := func(force bool) protocol.DeleteWorktreeResultMessage {
		return testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: path, Force: protocol.Ptr(force)},
			protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == path })
	}

	refused := deleteWorktree(false)
	if refused.Success || refused.ReasonKind != "dirty_worktree" || !protocol.Deref(refused.Forceable) {
		t.Errorf("deleting the dirty worktree = %+v; want a refusal as dirty that force can override", refused)
	}
	if _, err := os.Stat(filepath.Join(path, "local.txt")); err != nil {
		t.Errorf("the refused delete lost the local change: %v", err)
	}
	if !worktreeDeleteBranchExists(repo, "feat-dirty") {
		t.Error("the refused delete removed the branch")
	}
	if surface, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(surface.Worktrees), []string{path}) {
		t.Errorf("the worktree surface after the refused delete = %+v, %v; want %s still listed", surface, err, path)
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 1 || sessions[0].ID != session {
		t.Errorf("sessions after the refused delete = %+v, %v; want %s still there", sessions, err, session)
	}

	if forced := deleteWorktree(true); !forced.Success {
		t.Fatalf("the forced delete failed: %s", protocol.Deref(forced.Error))
	}
	for _, want := range []protocol.GitOperationStatus{protocol.GitOperationStatusFailed, protocol.GitOperationStatusSucceeded} {
		started := testworld.Await(app, protocol.EventGitOperationStarted, func(e protocol.GitOperationStartedMessage) bool {
			return e.Operation.Kind == protocol.GitOperationKindDeleteWorktree
		}).Operation
		finished := testworld.Await(app, protocol.EventGitOperationFinished, func(e protocol.GitOperationFinishedMessage) bool {
			return e.Operation.ID == started.ID
		}).Operation
		if started.ID == "" || started.Status != protocol.GitOperationStatusRunning || protocol.Deref(started.Path) != path {
			t.Errorf("the delete reported starting as %+v; want a running delete of %s", started, path)
		}
		if finished.Status != want || finished.FinishedAt == nil || finished.DurationMs == nil {
			t.Errorf("the delete reported finishing as %+v; want %s with its finish time and duration", finished, want)
		}
	}
	testworld.Await(app, protocol.EventWorktreeDeleted, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == path
	})
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == session
	})
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool {
		return e.Workspace.ID == "workspace-"+filepath.Base(path)
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the forced delete left the directory: %v", err)
	}
	if worktreeDeleteBranchExists(repo, "feat-dirty") {
		t.Error("the forced delete left the branch")
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 0 {
		t.Errorf("sessions after the forced delete = %+v, %v; want none", sessions, err)
	}
}

func worktreeDeleteBranchExists(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	return cmd.Run() == nil
}
