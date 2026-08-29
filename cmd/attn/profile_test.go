package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestCleanPlan(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantName  string
		wantForce bool
		wantErr   bool
	}{
		{name: "named profile", args: []string{"agent7"}, wantName: "agent7"},
		{name: "named profile uppercase normalizes", args: []string{"Agent7"}, wantName: "agent7"},
		{name: "dev is a normal named profile", args: []string{"dev"}, wantName: "dev"},
		{name: "force flag captured", args: []string{"agent7", "--force"}, wantName: "agent7", wantForce: true},
		{name: "force short flag", args: []string{"-f", "agent7"}, wantName: "agent7", wantForce: true},

		{name: "no name", args: nil, wantErr: true},
		{name: "default refused without force", args: []string{"default"}, wantErr: true},
		{name: "default allowed with force", args: []string{"default", "--force"}, wantName: "", wantForce: true},

		{name: "unknown flag", args: []string{"--nope", "agent7"}, wantErr: true},
		{name: "two names", args: []string{"a", "b"}, wantErr: true},
		{name: "invalid name", args: []string{"bad name"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotForce, err := cleanPlan(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("cleanPlan(%q) = (%q,%v,nil), want error", tc.args, gotName, gotForce)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanPlan(%q) unexpected error: %v", tc.args, err)
			}
			if gotName != tc.wantName || gotForce != tc.wantForce {
				t.Fatalf("cleanPlan(%q) = (%q,%v), want (%q,%v)", tc.args, gotName, gotForce, tc.wantName, tc.wantForce)
			}
		})
	}
}

func TestStopProfileDaemonNoPidFile(t *testing.T) {
	msg := stopProfileDaemon(profileResolved{DataDir: t.TempDir()})
	if !strings.Contains(msg, "no pid file") {
		t.Fatalf("stopProfileDaemon (no pid file) = %q, want a 'no pid file' note", msg)
	}
}

// A pid file no live daemon holds the lock on is stale and must NOT be signaled, even
// when its pid names a live (recycled) process. pid 1 is the canary: alive, never ours.
func TestStopProfileDaemonStalePidNotSignaled(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := stopProfileDaemon(profileResolved{DataDir: dir})
	if !strings.Contains(msg, "stale") {
		t.Fatalf("stopProfileDaemon (unlocked pid file naming live pid 1) = %q, want a 'stale' skip (it must not signal pid 1)", msg)
	}
}

// XDG_DATA_HOME is often a symlink (a home moved onto another volume), and
// /proc/<pid>/exe reports the resolved path: the app must still be recognized.
func TestSameExecutableAcceptsASymlinkedInstallRoot(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "share")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("XDG_DATA_HOME", linkRoot)

	configured := config.AppExecutableInTree(filepath.Join(linkRoot, "attn-lx"))
	running := config.AppExecutableInTree(filepath.Join(realRoot, "attn-lx"))
	if err := os.MkdirAll(filepath.Dir(running), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !sameExecutable(running, configured) {
		t.Fatalf("sameExecutable(%s, %s) = false, want true", running, configured)
	}
	if sameExecutable(config.AppExecutableInTree(filepath.Join(realRoot, "attn-other")), configured) {
		t.Fatal("sameExecutable() matched another profile's install tree")
	}
}

// `make install` replaces the install tree only when stop-app succeeded, so a
// profile with no app running must not report failure.
func TestStopProfileAppByPIDFileExitStatus(t *testing.T) {
	if msg, err := stopProfileAppByPIDFile(profileResolved{DataDir: t.TempDir()}); err != nil {
		t.Fatalf("stopProfileAppByPIDFile (no pid file) = %v, want a 'not running' success: %q", err, msg)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.pid"), []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := stopProfileAppByPIDFile(profileResolved{DataDir: dir}); err == nil {
		t.Fatalf("stopProfileAppByPIDFile (unreadable pid) = %q, want an error", msg)
	}
}
