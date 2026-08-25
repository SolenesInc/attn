package automode

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Detection is what a session launching here hands the classifier, so it is
// tested against a real repository rather than a parser.
func TestDetectFromRepoNamesTheRepoAndItsRemotes(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	run("remote", "add", "origin", "git@github.com:acme/widgets.git")
	run("remote", "add", "upstream", "https://github.com/upstream-org/widgets")

	slots, identities := DetectFromRepo(dir)
	values := slots["trusted_repo"]
	if len(values) != 3 {
		t.Fatalf("trusted_repo = %v, want the repo root and both remotes", values)
	}
	if filepath.Base(values[0]) != filepath.Base(dir) {
		t.Errorf("first entry = %q, want the repository root", values[0])
	}
	if values[1] != "github.com/acme/widgets" {
		t.Errorf("second entry = %q, want origin first", values[1])
	}
	if values[2] != "github.com/upstream-org/widgets" {
		t.Errorf("third entry = %q, want the other remote", values[2])
	}
	if len(identities) != 2 || identities[0] != "github.com/acme/widgets" {
		t.Errorf("identities = %v, want origin first for the visibility lookup", identities)
	}
}

func TestDetectFromRepoSaysNothingOutsideARepository(t *testing.T) {
	slots, identities := DetectFromRepo(t.TempDir())
	if slots != nil || identities != nil {
		t.Errorf("detected %v / %v outside a repository, want nothing", slots, identities)
	}
}
