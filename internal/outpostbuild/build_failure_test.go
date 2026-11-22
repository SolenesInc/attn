package outpostbuild_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/outpostbuild"
)

func TestFailedSourceBuildSaysItsCauseAndKeepsItsOutput(t *testing.T) {
	for _, tc := range []struct {
		output string
		cause  string
	}{
		{"cargo-missing-target.txt", "error[E0463]: can't find crate for `core`; note: the `x86_64-unknown-linux-gnu` target may not be installed; help: consider downloading the target with `rustup target add x86_64-unknown-linux-gnu`"},
		{"c-header-missing.txt", "internal/ghosttyvt/trackedref.go:6:10: fatal error: 'ghostty/vt.h' file not found"},
		{"go-type-error.txt", "internal/daemon/outpost_update.go:144:2: undefined: setOutpostUpdat"},
	} {
		t.Run(tc.output, func(t *testing.T) {
			printed, err := os.ReadFile(filepath.Join("testdata", tc.output))
			if err != nil {
				t.Fatal(err)
			}
			repo, commit := failingRepository(t, string(printed), "build:\n\t@cat printed.txt\n\t@exit 2\n")
			req := outpostbuild.Request{
				Build:      bridge.Build{Version: "0.15.0", SourceFingerprint: "git:" + commit, ProtocolVersion: "p"},
				Channel:    "source",
				Repository: repo,
				Cache:      t.TempDir(),
				Platform:   "linux/amd64",
			}

			_, err = outpostbuild.Acquire(context.Background(), req, func(string) {})

			if err == nil {
				t.Fatal("a build whose make fails acquired binaries")
			}
			if got := outpostbuild.Cause(err); got != tc.cause {
				t.Errorf("cause = %q\nwant    %q", got, tc.cause)
			}
			said := err.Error()
			if strings.Contains(said, "\n") {
				t.Errorf("the failure spans lines: %q", said)
			}
			_, log, ok := strings.Cut(said, "; full output in ")
			if !ok {
				t.Fatalf("the failure names no log: %q", said)
			}
			kept, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatalf("read the log the failure names: %v", readErr)
			}
			if !strings.HasPrefix(string(kept), string(printed)) {
				t.Errorf("the log holds %q, want the whole output %q", kept, printed)
			}
		})
	}
}

func TestEachFailedBuildKeepsItsOwnOutput(t *testing.T) {
	attempt := filepath.Join(t.TempDir(), "attempt.txt")
	t.Setenv("ATTEMPT", attempt)
	repo, commit := failingRepository(t, "", "build:\n\t@cat \"$$ATTEMPT\"\n\t@exit 2\n")
	req := outpostbuild.Request{
		Build:      bridge.Build{Version: "0.15.0", SourceFingerprint: "git:" + commit, ProtocolVersion: "p"},
		Channel:    "source",
		Repository: repo,
		Cache:      t.TempDir(),
		Platform:   "linux/amd64",
	}
	fail := func(printed string) string {
		t.Helper()
		if err := os.WriteFile(attempt, []byte(printed), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := outpostbuild.Acquire(context.Background(), req, func(string) {})
		if err == nil {
			t.Fatal("a build whose make fails acquired binaries")
		}
		_, log, ok := strings.Cut(err.Error(), "; full output in ")
		if !ok {
			t.Fatalf("the failure names no log: %q", err)
		}
		return log
	}

	first := fail("error: the first outpost's failure\n")
	fail("error: the second outpost's failure\n")

	kept, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read the first failure's log: %v", err)
	}
	if !strings.HasPrefix(string(kept), "error: the first outpost's failure\n") {
		t.Errorf("the first failure's log holds %q", kept)
	}
}

func failingRepository(t *testing.T, printed, makefile string) (repo, commit string) {
	t.Helper()
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "cargo"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "printed.txt"), []byte(printed), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "failing build")
	return repo, git("rev-parse", "HEAD")
}
