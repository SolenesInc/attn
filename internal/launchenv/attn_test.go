package launchenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveAttnExecutable_PrefersInstanceWrapperOverStalePath(t *testing.T) {
	root := t.TempDir()
	instanceDir := filepath.Join(root, "attn-instance")
	staleDir := filepath.Join(root, "stale-attn")
	for _, dir := range []string{instanceDir, staleDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	for _, path := range []string{filepath.Join(instanceDir, "attn"), filepath.Join(staleDir, "attn")} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	instanceWrapper := filepath.Join(instanceDir, "attn")
	t.Setenv(wrapperPathEnv, instanceWrapper)
	t.Setenv("PATH", staleDir)

	if got := ActiveAttnExecutable(); got != instanceWrapper {
		t.Fatalf("ActiveAttnExecutable() = %q, want active instance wrapper %q", got, instanceWrapper)
	}
}

func TestWithActiveAttnFirst_PrependsAndDeduplicatesInstanceDirectory(t *testing.T) {
	root := t.TempDir()
	instanceDir := filepath.Join(root, "attn-instance")
	staleDir := filepath.Join(root, "stale-attn")
	otherDir := filepath.Join(root, "other-tools")
	env := []string{
		"PATH=" + strings.Join([]string{staleDir, instanceDir, otherDir, instanceDir + string(filepath.Separator)}, string(os.PathListSeparator)),
		"UNCHANGED=value",
	}

	got := WithActiveAttnFirst(env, filepath.Join(instanceDir, "attn"))
	wantPath := strings.Join([]string{instanceDir, staleDir, otherDir}, string(os.PathListSeparator))
	if got[0] != "PATH="+wantPath {
		t.Fatalf("PATH entry = %q, want %q", got[0], "PATH="+wantPath)
	}
	if got[1] != "UNCHANGED=value" {
		t.Fatalf("unrelated environment changed: %#v", got)
	}
}
