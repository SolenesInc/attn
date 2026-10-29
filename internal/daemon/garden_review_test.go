package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	project := filepath.Join(toolHome, ".claude", "projects", "work")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "native-1.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.store.Remove("sess-a")
	d.store.Add(&protocol.Session{
		ID: "sess-a", Directory: cwd, Agent: protocol.SessionAgentClaude, State: protocol.SessionStateIdle, ProfileID: defaultProfileID(t, d.store),
	})
	d.store.SetResumeSessionID("sess-a", "native-1")
	d.store.SetLaunchIntent("sess-a", store.LaunchIntent{})
	seed := plant(t, d, protocol.SeedPlantMessage{Body: protocol.Ptr("Complete the assigned work."), Title: "Resumable old work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	d.closeSession("sess-a", store.SessionClose{By: store.SessionClosedByUser})
	d.gardenNow = func() time.Time { return now }

	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	item := capture.items[seed.ID]
	if !slices.Equal(item.Actions, []string{"resume", "handover", "send_to_chief", "keep_growing", "park", "harvest", "wither"}) {
		t.Fatalf("resumable actions = %v", item.Actions)
	}
}
