package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/ptyworker"
)

// Keeps every config-path lookup in this package inside a temp dir, so no test can
// resolve production ~/.attn. See AGENTS.md "Test safety".
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-cmd-test")
	if err != nil {
		panic(err)
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestProfileOriginRoundTrip(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "not-yet-created")
	want := profileOrigin{
		Worktree:   "/Users/victor/projects/victor/attn--wily-raccoon",
		Branch:     "wily-raccoon",
		RecordedAt: "2026-08-02T00:00:00Z",
	}
	if err := writeProfileOrigin(dataDir, want); err != nil {
		t.Fatalf("writeProfileOrigin() error: %v", err)
	}
	got := readProfileOrigin(dataDir)
	if got == nil {
		t.Fatal("readProfileOrigin() = nil, want the recorded origin")
	}
	if *got != want {
		t.Fatalf("origin = %+v, want %+v", *got, want)
	}
}

func TestReadProfileOriginAbsentOrUnusable(t *testing.T) {
	tests := []struct {
		name    string
		content string
		write   bool
	}{
		{name: "no origin file", write: false},
		{name: "malformed json", content: "{not json", write: true},
		{name: "blank worktree", content: `{"worktree":"  "}`, write: true},
		{name: "missing worktree key", content: `{"branch":"x"}`, write: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if tc.write {
				if err := os.WriteFile(originPath(dataDir), []byte(tc.content), 0600); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			if got := readProfileOrigin(dataDir); got != nil {
				t.Fatalf("readProfileOrigin() = %+v, want nil", *got)
			}
		})
	}
}

func TestOriginPathIsInsideDataDir(t *testing.T) {
	dataDir := t.TempDir()
	if got, want := originPath(dataDir), filepath.Join(dataDir, "origin.json"); got != want {
		t.Fatalf("originPath() = %q, want %q", got, want)
	}
}

func TestCountLiveWorkers(t *testing.T) {
	dataDir := t.TempDir()
	if got := countLiveWorkers(dataDir); got != 0 {
		t.Fatalf("countLiveWorkers() = %d on an empty data dir, want 0", got)
	}

	write := func(session string, pid int) {
		path := filepath.Join(dataDir, "workers", "d-1", "registry", session+".json")
		if err := ptyworker.WriteRegistryAtomic(path, ptyworker.RegistryEntry{
			Version: 1, SessionID: session, WorkerPID: pid,
		}); err != nil {
			t.Fatalf("write registry: %v", err)
		}
	}
	write("alive", os.Getpid())
	write("dead", -1)

	if got := countLiveWorkers(dataDir); got != 1 {
		t.Fatalf("countLiveWorkers() = %d, want 1 (dead entries must not count)", got)
	}
}

func TestSocketLiveOnMissingSocket(t *testing.T) {
	if socketLive("") {
		t.Error("socketLive(\"\") = true")
	}
	if socketLive(filepath.Join(t.TempDir(), "absent.sock")) {
		t.Error("socketLive() = true for a socket that does not exist")
	}
}

func TestSocketLiveIgnoresStaleSocketFile(t *testing.T) {
	stale := filepath.Join(t.TempDir(), "attn.sock")
	if err := os.WriteFile(stale, nil, 0600); err != nil {
		t.Fatalf("write stale socket file: %v", err)
	}
	if socketLive(stale) {
		t.Error("socketLive() = true for a plain file masquerading as a socket")
	}
}

func TestSummarizeReapOrdersByOutcome(t *testing.T) {
	got := summarizeReap(map[ptyworker.ReapOutcome]int{
		ptyworker.ReapUnidentified: 1,
		ptyworker.ReapRemoved:      2,
		ptyworker.ReapAlreadyGone:  3,
	})
	want := "2 removed, 3 already gone, 1 unidentified"
	if got != want {
		t.Fatalf("summarizeReap() = %q, want %q", got, want)
	}
}

func TestSummarizeReapSkipsZeroCounts(t *testing.T) {
	got := summarizeReap(map[ptyworker.ReapOutcome]int{ptyworker.ReapRemoved: 1})
	if got != "1 removed" {
		t.Fatalf("summarizeReap() = %q, want %q", got, "1 removed")
	}
}
