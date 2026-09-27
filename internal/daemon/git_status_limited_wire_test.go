package daemon_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func slowUntrackedScanGit(t *testing.T, repo string) (fullScans func() int) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "full-scans")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$PWD" = %q ]; then
  for arg in "$@"; do
    if [ "$arg" = "--untracked-files=all" ]; then echo full >> %q; exec sleep 3600; fi
  done
fi
exec %q "$@"
`, repo, log, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "full\n")
	}
}

func TestARepositoryWhoseFullStatusTimesOutIsShownTrackedOnlyAndStaysSo(t *testing.T) {
	repo := newRepo(t, "monorepo")
	nested := filepath.Join(repo, ".claude", "worktrees", "agent")
	runGit(t, repo, "worktree", "add", "-b", "agent/one", nested)
	for name, body := range map[string]string{"README.md": "edited\n", "scratch.txt": "untracked\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fullScans := slowUntrackedScanGit(t, repo)
	w := newWorld(t)
	app := w.App()

	status := testworld.Request(app, protocol.SubscribeGitStatusMessage{Cmd: protocol.CmdSubscribeGitStatus, Directory: repo},
		protocol.EventGitStatusUpdate, func(s protocol.GitStatusUpdateMessage) bool { return s.Directory == repo })
	if !protocol.Deref(status.Limited) || protocol.Deref(status.Mode) != "tracked_only" || protocol.Deref(status.LimitedReason) == "" {
		t.Errorf("after the full status timed out the repository shows limited=%t mode=%q reason=%q, want tracked-only with a reason",
			protocol.Deref(status.Limited), protocol.Deref(status.Mode), protocol.Deref(status.LimitedReason))
	}
	if len(status.Unstaged) != 1 || status.Unstaged[0].Path != "README.md" || len(status.Untracked) != 0 {
		t.Errorf("the tracked-only status lists unstaged %+v and untracked %+v, want README.md alone", status.Unstaged, status.Untracked)
	}

	commitFile(t, repo, "README.md", "committed\n")
	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: nested},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == nested })
	if !deleted.Success {
		t.Fatalf("deleting the nested worktree: %s", protocol.Deref(deleted.Error))
	}
	refreshed := testworld.Await(app, protocol.EventGitStatusUpdate, func(s protocol.GitStatusUpdateMessage) bool {
		return s.Directory == repo && len(s.Unstaged) == 0
	})
	if !protocol.Deref(refreshed.Limited) {
		t.Errorf("the refresh after a limited status reads limited=false, want it still tracked-only")
	}
	if scans := fullScans(); scans != 1 {
		t.Errorf("git was asked for a full untracked scan %d times, want only the first before the repository was known slow", scans)
	}
}
