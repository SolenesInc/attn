package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestArmedSeedsAreReadPastTheSnapshotPage(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	for i := 0; i < gardenSnapshotLimit+1; i++ {
		id := fmt.Sprintf("s-%06d", i)
		pr := "github.com:victorarias/attn#275"
		if i == gardenSnapshotLimit {
			pr = "github.com:victorarias/attn#276"
		}
		seed := garden.Seed{
			ProfileID: defaultProfileID(t, d.store),
			ID:        id, Title: id, StepSlug: id, Status: garden.StatusPlanted,
			StateChangedAt: "2026-09-12T00:00:00Z", Edges: []garden.Edge{}, Vars: []garden.Var{},
			HarvestWhen: &garden.HarvestCondition{PullRequest: pr},
		}
		body, encodeErr := seed.Encode()
		if encodeErr != nil {
			t.Fatalf("encode %s: %v", id, encodeErr)
		}
		if _, putErr := d.store.PutDocument(*schema, id, body, time.Now(), nil); putErr != nil {
			t.Fatalf("put %s: %v", id, putErr)
		}
	}

	seeds, err := d.armedSeeds()
	if err != nil {
		t.Fatalf("armed seeds: %v", err)
	}
	if len(seeds) != gardenSnapshotLimit+1 {
		t.Fatalf("armed seeds returned %d, want %d", len(seeds), gardenSnapshotLimit+1)
	}
	if got := seeds[len(seeds)-1].ID; got != fmt.Sprintf("s-%06d", gardenSnapshotLimit) {
		t.Fatalf("last armed seed is %s", got)
	}
}
