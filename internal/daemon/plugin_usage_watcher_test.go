package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

func piUsageFixtureLines(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "usage", "pi-0.83.0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.SplitAfter(bytes.TrimRight(data, "\n"), []byte("\n"))
}

func TestPiTranscriptThatAlreadyExistsKeepsItsHeadBaseline(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-resume", Label: "pi work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	lines := piUsageFixtureLines(t)
	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	if err := os.WriteFile(path, bytes.Join(lines[:5], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	d.seedPluginUsageBaseline("pi-resume", path)
	tracker := newSessionUsageTrackerAt(d, "pi-resume", "pi", path, transcript.NewReportedUsageSourceResolver(path))
	tracker.Reconcile()

	decorated := &protocol.Session{ID: "pi-resume"}
	d.decorateSessionWithCost(decorated)
	if decorated.Usage != nil {
		t.Fatalf("usage = %+v, want a resumed session to bill nothing it inherited", decorated.Usage)
	}
}
