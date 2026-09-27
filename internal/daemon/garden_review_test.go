package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

func TestGardenReviewOffersResumeOnlyWithUsableContinuation(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-garden.DefaultStaleWindow)
	d.gardenNow = func() time.Time { return old }
	cwd := t.TempDir()
	toolHome := t.TempDir()
	t.Setenv(toolhome.EnvVar, toolHome)
	conversation := filepath.Join(toolHome, ".copilot", "session-state", "native-1")
	if err := os.MkdirAll(conversation, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conversation, "events.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.store.Remove("sess-a")
	d.store.Add(&protocol.Session{
		ID: "sess-a", Directory: cwd, Agent: protocol.SessionAgentCopilot, State: protocol.SessionStateIdle,
	})
	d.store.SetResumeSessionID("sess-a", "native-1")
	d.store.SetLaunchIntent("sess-a", store.LaunchIntent{})
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Resumable old work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	d.closeSession("sess-a", store.SessionClose{By: store.SessionClosedByUser})
	d.gardenNow = func() time.Time { return now }

	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	item := capture.items[seed.ID]
	if !slices.Equal(item.Actions, []string{"resume", "handover", "keep_growing", "park", "harvest", "wither"}) {
		t.Fatalf("resumable actions = %v", item.Actions)
	}
}

func TestGardenReviewCapturePagesPastTheGardenSnapshotLimit(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	d.gardenNow = func() time.Time { return now.Add(-garden.DefaultStaleWindow) }
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	for i := 0; i < docstore.MaxLimit+1; i++ {
		id := fmt.Sprintf("s-%06x", i)
		seed := garden.Seed{
			ID: id, Title: id, Status: garden.StatusGrowing, StepSlug: id,
			StateChangedAt: formatGardenTime(d.gardenNow()), Edges: []garden.Edge{}, Vars: []garden.Var{},
		}
		body, _ := seed.Encode()
		if _, err := d.store.PutDocument(*schema, id, body, d.gardenNow(), nil); err != nil {
			t.Fatalf("put seed %s: %v", id, err)
		}
	}
	d.gardenNow = func() time.Time { return now }
	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	if len(capture.candidates) != docstore.MaxLimit+1 {
		t.Fatalf("candidates = %d, want %d", len(capture.candidates), docstore.MaxLimit+1)
	}
}
