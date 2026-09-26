package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/git"
)

type sweepRepo struct {
	t    *testing.T
	root string
	main string
}

func newSweepRepo(t *testing.T) *sweepRepo {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, root, "init", "--bare", "origin.git")
	runGitDaemon(t, main, "init", "-b", "main")
	runGitDaemon(t, main, "remote", "add", "origin", origin)

	repo := &sweepRepo{t: t, root: root, main: git.CanonicalizePath(main)}
	repo.commitOnMain("seed", "seed\n", "seed")
	runGitDaemon(t, main, "push", "-u", "origin", "main")
	return repo
}

func (r *sweepRepo) commitIn(dir, file, content, message string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
	runGitDaemon(r.t, dir, "add", file)
	runGitDaemon(r.t, dir, "commit", "-m", message)
	return strings.TrimSpace(gitOutput(r.t, dir, "rev-parse", "HEAD"))
}

func (r *sweepRepo) commitOnMain(file, content, message string) string {
	return r.commitIn(filepath.Join(r.root, "main"), file, content, message)
}

func (r *sweepRepo) worktree(name, branch, from string) string {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	runGitDaemon(r.t, filepath.Join(r.root, "main"), "worktree", "add", "-b", branch, path, from)
	return git.CanonicalizePath(path)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}

func sweepDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
}
