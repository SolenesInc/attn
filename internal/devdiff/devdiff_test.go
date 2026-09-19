package devdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func commitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=devdiff", "-c", "user.email=devdiff@example.test", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSinceSeesCommittedUncommittedAndUntrackedWork(t *testing.T) {
	dir := t.TempDir()
	commitFixture(t, dir, "init", "-q", "-b", "next")
	write(t, dir, "kept.go", "package a\n")
	write(t, dir, "edited.go", "package a\n")
	commitFixture(t, dir, "add", ".")
	commitFixture(t, dir, "commit", "-q", "-m", "base")
	commitFixture(t, dir, "switch", "-q", "-c", "work")
	write(t, dir, "committed name.go", "package a\n")
	commitFixture(t, dir, "add", ".")
	commitFixture(t, dir, "commit", "-q", "-m", "work")
	write(t, dir, "edited.go", "package a\n\n// why\n")
	write(t, dir, "untracked.go", "package a\n")
	t.Chdir(dir)

	change, err := Since("next")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := change.Paths()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	if want := []string{"committed name.go", "edited.go", "untracked.go"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	diff, err := change.UnifiedDiff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+// why") {
		t.Fatalf("diff misses the uncommitted edit:\n%s", diff)
	}
}

func TestSinceNamesTheFixWhenTheBaseIsMissing(t *testing.T) {
	dir := t.TempDir()
	commitFixture(t, dir, "init", "-q", "-b", "next")
	write(t, dir, "a.go", "package a\n")
	commitFixture(t, dir, "add", ".")
	commitFixture(t, dir, "commit", "-q", "-m", "base")
	t.Chdir(dir)

	_, err := Since("origin/absent")
	if err == nil || !strings.Contains(err.Error(), "DIFF_BASE=<ref>") || !strings.Contains(err.Error(), "origin/absent") {
		t.Fatalf("err = %v", err)
	}
}
