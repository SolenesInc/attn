package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func armSeed(t *testing.T, d *Daemon, seedID string, condition garden.HarvestCondition) {
	t.Helper()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read %s: %v", seedID, err)
	}
	seed.HarvestWhen = &condition
	if _, err := d.writeSeed(*schema, seed, doc.Rev, FactGardenHarvestWhenChanged); err != nil {
		t.Fatalf("write %s: %v", seedID, err)
	}
}

func TestArmedSeedsAreFoundByTheirPullRequest(t *testing.T) {
	d := newGardenDaemon(t)
	armed := plant(t, d, protocol.SeedPlantMessage{Title: "ship the protocol"})
	other := plant(t, d, protocol.SeedPlantMessage{Title: "ship the daemon"})
	plant(t, d, protocol.SeedPlantMessage{Title: "nothing to wait on"})

	armSeed(t, d, armed.ID, garden.HarvestCondition{
		PullRequest: "github.com:victorarias/attn#113",
		URL:         "https://github.com/victorarias/attn/pull/113",
		SetAt:       "2026-09-02T00:21:00Z",
	})
	armSeed(t, d, other.ID, garden.HarvestCondition{
		PullRequest: "github.com:victorarias/attn#114",
		URL:         "https://github.com/victorarias/attn/pull/114",
		SetAt:       "2026-09-02T00:22:00Z",
	})

	ids := func(t *testing.T, filters []docstore.Filter) []string {
		t.Helper()
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionSeeds,
			Filters:    filters,
		})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		out := make([]string, 0, len(read.Documents))
		for _, doc := range read.Documents {
			out = append(out, doc.ID)
		}
		return out
	}

	one := ids(t, []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpEq, Value: "github.com:victorarias/attn#113"}})
	if len(one) != 1 || one[0] != armed.ID {
		t.Fatalf("a query for one pull request returned %v, want just %s", one, armed.ID)
	}

	all := ids(t, []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpGt, Value: ""}})
	if len(all) != 2 {
		t.Fatalf("a query for every armed seed returned %v, want the two armed ones", all)
	}
}

func TestSeedShowCarriesTheHarvestCondition(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "ship the protocol"})
	if before := show(t, d, seed.ID); before.Seed.HarvestWhen != nil {
		t.Fatalf("an unarmed seed carries a condition on the wire: %+v", before.Seed.HarvestWhen)
	}

	armSeed(t, d, seed.ID, garden.HarvestCondition{
		PullRequest:  "github.com:victorarias/attn#113",
		URL:          "https://github.com/victorarias/attn/pull/113",
		SetAt:        "2026-09-02T00:21:00Z",
		SetBySession: "sess-a",
		SetByMember:  "trellis",
	})

	got := show(t, d, seed.ID).Seed.HarvestWhen
	if got == nil {
		t.Fatalf("the wire lost the harvest condition")
	}
	if got.PullRequest != "github.com:victorarias/attn#113" || got.URL != "https://github.com/victorarias/attn/pull/113" {
		t.Fatalf("the wire changed the pull request: %+v", got)
	}
	if protocol.Deref(got.SetBySession) != "sess-a" || protocol.Deref(got.SetByMember) != "trellis" {
		t.Fatalf("the wire lost who armed the seed: %+v", got)
	}
	if got.SetAt != "2026-09-02T00:21:00Z" {
		t.Fatalf("the wire changed when it was armed: %+v", got)
	}
}
