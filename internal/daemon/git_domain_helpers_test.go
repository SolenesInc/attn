package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
)

func TestResolveMainRepoFindsARepositoryBehindItsLogicalPath(t *testing.T) {
	parent := t.TempDir()
	logical := filepath.Join(parent, "repo")
	actual := filepath.Join(parent, "container", "repo")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, actual, "init", "-b", "main")
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))

	resolved, err := d.resolveMainRepo(context.Background(), gitTaskRepositoryInfo, gitInteractive, logical)
	if err != nil {
		t.Fatal(err)
	}
	if attngit.CanonicalizePath(resolved) != attngit.CanonicalizePath(actual) {
		t.Fatalf("resolved repository=%q, want %q", resolved, attngit.CanonicalizePath(actual))
	}
}
