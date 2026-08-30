package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func writeCodexInteractiveRollout(t *testing.T, codexHome, nativeID, cwd string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "05", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("rollout-%s-%s.jsonl", at.UTC().Format("2006-01-02T15-04-05"), nativeID))
	line := fmt.Sprintf(
		`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","timestamp":"%s","cwd":"%s","source":"cli"}}`+"\n",
		at.UTC().Format(time.RFC3339Nano), nativeID, at.UTC().Format(time.RFC3339Nano), cwd,
	)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes rollout: %v", err)
	}
	return path
}

func TestResolveTranscriptPathForSession_PrefersPersistedExactPath(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	now := time.Now()

	own := writeCodexInteractiveRollout(t, codexHome, "native-own", cwd, now.Add(-time.Minute))
	newerNeighbor := writeCodexInteractiveRollout(t, codexHome, "native-neighbor", cwd, now.Add(-5*time.Second))

	d.store.Add(&protocol.Session{
		ID:        "sess",
		Label:     "sess",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
	})
	if changed, err := d.store.TransitionSessionConversation("sess", "native-own", own); err != nil || !changed {
		t.Fatalf("seed binding: changed=%v err=%v", changed, err)
	}
	lookups := 0
	d.transcriptResumeLookup = func(protocol.SessionAgent, string) string {
		lookups++
		return newerNeighbor
	}

	got := d.resolveTranscriptPathForSession(d.store.Get("sess"), "")
	if got != own {
		t.Fatalf("resolveTranscriptPathForSession() = %q, want own rollout %q (newer neighbor=%q)", got, own, newerNeighbor)
	}
	if lookups != 0 {
		t.Fatalf("resolution performed %d fallback lookups", lookups)
	}
}

func TestResolveTranscriptPathForSession_RejectsCWDGuessWithoutNativeID(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	now := time.Now()

	neighbor := writeCodexInteractiveRollout(t, codexHome, "native-only", cwd, now.Add(-time.Minute))

	d.store.Add(&protocol.Session{
		ID:        "sess",
		Label:     "sess",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
	})

	got := d.resolveTranscriptPathForSession(d.store.Get("sess"), "")
	if got != "" {
		t.Fatalf("resolveTranscriptPathForSession() = %q, want no exact path (neighbor=%q)", got, neighbor)
	}
}

func TestResolveTranscriptPathForSession_DoesNotDiscoverWhenBoundPathIsMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.Add(&protocol.Session{ID: "sess", Agent: protocol.SessionAgentCodex})
	if changed, err := d.store.TransitionSessionConversation("sess", "native-own", filepath.Join(t.TempDir(), "missing.jsonl")); err != nil || !changed {
		t.Fatalf("seed binding: changed=%v err=%v", changed, err)
	}
	lookups := 0
	d.transcriptResumeLookup = func(protocol.SessionAgent, string) string {
		lookups++
		return "/neighbor.jsonl"
	}

	if got := d.resolveTranscriptPathForSession(d.store.Get("sess"), ""); got != "" {
		t.Fatalf("resolveTranscriptPathForSession() = %q, want unavailable", got)
	}
	if lookups != 0 {
		t.Fatalf("resolution performed %d fallback lookups", lookups)
	}
}
