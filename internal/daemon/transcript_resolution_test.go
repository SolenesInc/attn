package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestResolveStopTranscriptPath_RejectsAReportedNeighborWhenBoundPathIsMissing(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	neighbor := writeCodexInteractiveRollout(t, codexHome, "native-neighbor", cwd, time.Now())

	d.store.Add(&protocol.Session{ID: "sess", Agent: protocol.SessionAgentCodex, Directory: cwd})
	if changed, err := d.store.TransitionSessionConversation("sess", "native-own", missing); err != nil || !changed {
		t.Fatalf("bind missing transcript: changed=%v err=%v", changed, err)
	}

	if got := d.resolveStopTranscriptPath(d.store.Get("sess"), neighbor); got != "" {
		t.Fatalf("stop resolved to neighbor %q when bound transcript %q is missing", got, missing)
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
