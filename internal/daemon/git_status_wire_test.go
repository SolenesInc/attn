package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFileDiffBetweenPinnedRefsIgnoresTheWorkingTree(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	v1 := commitFile(t, repo, "src/file.ts", "v1")
	v2 := commitFile(t, repo, "src/file.ts", "v2")
	if err := os.WriteFile(filepath.Join(repo, "src/file.ts"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		name, base, head, original, modified string
	}{
		{"between two revisions", v1, v2, "v1", "v2"},
		{"where the file is absent", before, before, "", ""},
	} {
		requestID := "diff " + row.name
		result := testworld.Request(app, protocol.GetFileDiffMessage{
			Cmd: protocol.CmdGetFileDiff, Directory: repo, Path: "src/file.ts",
			BaseRef: protocol.Ptr(row.base), HeadRef: protocol.Ptr(row.head), RequestID: protocol.Ptr(requestID),
		}, protocol.EventFileDiffResult, func(r protocol.FileDiffResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
		if !result.Success || result.Original != row.original || result.Modified != row.modified {
			t.Errorf("the diff %s = %q -> %q (%s); want %q -> %q", row.name, result.Original, result.Modified, protocol.Deref(result.Error), row.original, row.modified)
		}
	}
}

func TestGitStatusReportsStagedUnstagedAndUntrackedFiles(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "src/App.tsx", "one\ntwo\nthree\n")
	for name, body := range map[string]string{
		"src/App.tsx": "one\nTWO\nthree\n", "src/new.ts": "export {}\n", "untracked.txt": "scratch\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "add", "src/new.ts")

	status := testworld.Request(app, protocol.SubscribeGitStatusMessage{Cmd: protocol.CmdSubscribeGitStatus, Directory: repo},
		protocol.EventGitStatusUpdate, func(s protocol.GitStatusUpdateMessage) bool { return s.Directory == repo })
	for _, row := range []struct {
		kind    string
		changes []protocol.GitFileChange
		want    protocol.GitFileChange
	}{
		{"staged", status.Staged, protocol.GitFileChange{Path: "src/new.ts", Status: "added"}},
		{"unstaged", status.Unstaged, protocol.GitFileChange{Path: "src/App.tsx", Status: "modified"}},
		{"untracked", status.Untracked, protocol.GitFileChange{Path: "untracked.txt", Status: "untracked"}},
	} {
		if len(row.changes) != 1 || row.changes[0].Path != row.want.Path || row.changes[0].Status != row.want.Status {
			t.Errorf("%s changes = %+v; want only %s %s", row.kind, row.changes, row.want.Status, row.want.Path)
		}
	}
}

func TestDeletingAWorktreeInsideARepositoryRefreshesItsStatusAtOnce(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		repo := newRepo(t, "shop")
		nested := filepath.Join(repo, ".claude", "worktrees", "agent")
		runGit(t, repo, "worktree", "add", "-b", "agent/one", nested)
		sibling := repo + "-feat"
		runGit(t, repo, "worktree", "add", "-b", "feat", sibling)
		testworld.Request(app, protocol.SubscribeGitStatusMessage{Cmd: protocol.CmdSubscribeGitStatus, Directory: repo},
			protocol.EventGitStatusUpdate, func(s protocol.GitStatusUpdateMessage) bool { return s.Directory == repo })
		if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("later\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		for _, row := range []struct {
			name    string
			path    string
			refresh bool
		}{
			{"a worktree beside the repository", sibling, false},
			{"a worktree inside the repository", nested, true},
		} {
			deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: row.path},
				protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == row.path })
			if !deleted.Success {
				t.Fatalf("deleting %s: %s", row.name, protocol.Deref(deleted.Error))
			}
			w.advance(time.Second)
			untracked := gitStatusWireUntracked(app, repo)
			if refreshed := slices.Contains(untracked, "later.txt"); refreshed != row.refresh {
				t.Errorf("a second after deleting %s the repository's status lists untracked %v; want it refreshed: %v", row.name, untracked, row.refresh)
			}
		}
	})
}

func gitStatusWireUntracked(app *testworld.Peer, dir string) []string {
	var untracked []string
	for _, e := range app.Received() {
		if e.Event != protocol.EventGitStatusUpdate || protocol.Deref(e.Directory) != dir {
			continue
		}
		untracked = nil
		for _, change := range e.Untracked {
			untracked = append(untracked, change.Path)
		}
	}
	return untracked
}
