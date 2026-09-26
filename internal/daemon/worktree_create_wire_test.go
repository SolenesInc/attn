package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAWorktreeBelongsToTheRepositoryItWasCreatedFrom(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "container", "hurdy-gurdy")
	if err := os.MkdirAll(filepath.Join(main, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "init", "-b", "main")
	commitFile(t, main, "README.md", "hurdy-gurdy\n")
	runGit(t, main, "branch", "feature/fun")
	otherWorktree := main + "--feat-auto-bump-yt-dlp--fork-hurdy-gurdy"
	runGit(t, main, "worktree", "add", "-b", "feat/auto-bump-yt-dlp", otherWorktree)

	create := func(from, branch string) protocol.CreateWorktreeMessage {
		return protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: from, Branch: branch}
	}
	for _, row := range []struct {
		name  string
		cmd   any
		wants string
	}{
		{"its logical path", create(filepath.Join(root, "hurdy-gurdy"), "feat/logical"), main + "--feat-logical"},
		{"a subdirectory", create(filepath.Join(main, "app"), "feat/subdir"), main + "--feat-subdir"},
		{"another worktree", create(otherWorktree, "fork/fun"), main + "--fork-fun"},
		{"another worktree, onto an existing branch", protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: otherWorktree, Branch: "feature/fun",
		}, main + "--feature-fun"},
	} {
		result := testworld.Request(app, row.cmd, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if !result.Success || protocol.Deref(result.Path) != row.wants {
			t.Errorf("creating a worktree from %s = %q (%s); want %s", row.name, protocol.Deref(result.Path), protocol.Deref(result.Error), row.wants)
			continue
		}
		created := testworld.Await(app, protocol.EventWorktreeCreated, func(e protocol.WebSocketEvent) bool {
			return len(e.Worktrees) == 1 && e.Worktrees[0].Path == row.wants
		}).Worktrees[0]
		if created.MainRepo != main {
			t.Errorf("the worktree created from %s belongs to %q; want the main checkout %s", row.name, created.MainRepo, main)
		}
		if _, err := os.Stat(filepath.Join(row.wants, "README.md")); err != nil {
			t.Errorf("the worktree created from %s is not a checkout: %v", row.name, err)
		}
	}
}

func TestAWorktreeWhoseDirectoryVanishedLeavesTheListAndItsPlaceCanBeReused(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	vanished := createWorktree(t, app, repo, "feat-vanished")
	reused := createWorktree(t, app, repo, "feat-reused")
	for _, path := range []string{vanished, reused} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}

	listed := testworld.Request(app, protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: repo},
		protocol.EventWorktreesUpdated, func(protocol.WebSocketEvent) bool { return true })
	if paths := worktreeCreatePaths(listed.Worktrees); len(paths) != 0 {
		t.Errorf("list_worktrees after the directories vanished = %v; want neither", paths)
	}
	if surface, err := cli.WorktreeList(repo, 0); err != nil || len(surface.Worktrees) != 0 {
		t.Errorf("the worktree surface after the directories vanished = %+v, %v; want neither", surface, err)
	}

	result := testworld.Request(app, protocol.CreateWorktreeFromBranchMessage{
		Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "feat-reused", Path: protocol.Ptr(reused),
	}, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
	if !result.Success || protocol.Deref(result.Path) != reused {
		t.Fatalf("checking feat-reused out where its vanished worktree was = %q (%s); want %s", protocol.Deref(result.Path), protocol.Deref(result.Error), reused)
	}
	if _, err := os.Stat(filepath.Join(reused, "README.md")); err != nil {
		t.Errorf("the worktree was not recreated: %v", err)
	}
	if surface, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(surface.Worktrees), []string{reused}) {
		t.Errorf("the worktree surface after recreating = %+v, %v; want only %s", surface, err, reused)
	}
}

func worktreeCreatePaths(worktrees []protocol.Worktree) []string {
	var paths []string
	for _, wt := range worktrees {
		paths = append(paths, wt.Path)
	}
	return paths
}
