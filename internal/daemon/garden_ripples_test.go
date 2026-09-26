package daemon

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type rippleGarden struct {
	seededNudgeGarden
	blocker, dependent protocol.Seed
}

func newRippleGarden(t *testing.T) rippleGarden {
	t.Helper()
	fixture := newSeededNudgeGarden(t)
	blocker := plantUnder(t, fixture, "lay the pipe")
	dependent := plantUnder(t, fixture, "run water through it")
	mustLink(t, fixture.d, blocker.ID, garden.EdgeBlocks, dependent.ID)
	return rippleGarden{seededNudgeGarden: fixture, blocker: blocker, dependent: dependent}
}

func plantUnder(t *testing.T, fixture seededNudgeGarden, title string) protocol.Seed {
	t.Helper()
	return plant(t, fixture.d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: title, PartOf: protocol.Ptr(fixture.crown.ID),
	})
}

func unblockedIDs(t *testing.T, resp protocol.Response) []string {
	t.Helper()
	if !resp.Ok {
		t.Fatalf("transition: %v", protocol.Deref(resp.Error))
	}
	out := make([]string, 0, len(resp.SeedTransitionResult.Unblocked))
	for _, seed := range resp.SeedTransitionResult.Unblocked {
		out = append(out, seed.ID)
	}
	slices.Sort(out)
	return out
}

func TestSeedRipples_TheGraphIsReadPastTheSnapshotPage(t *testing.T) {
	fixture := newRippleGarden(t)
	schema, err := fixture.d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	put := func(id string, at time.Time, status string, edges []garden.Edge, tenderSession ...string) {
		t.Helper()
		if edges == nil {
			edges = []garden.Edge{}
		}
		seed := garden.Seed{
			ID: id, Title: id, StepSlug: id, Status: status,
			StateChangedAt: formatGardenTime(at), Edges: edges, Vars: []garden.Var{},
		}
		if len(tenderSession) != 0 {
			seed.TenderSession = tenderSession[0]
		}
		body, encodeErr := seed.Encode()
		if encodeErr != nil {
			t.Fatalf("encode %s: %v", id, encodeErr)
		}
		if _, putErr := fixture.d.store.PutDocument(*schema, id, body, at, nil); putErr != nil {
			t.Fatalf("put %s: %v", id, putErr)
		}
	}
	put("s-0000a1", base, garden.StatusPlanted, []garden.Edge{{Kind: garden.EdgeBlocks, To: "s-0000b1"}})
	put("s-0000a2", base.Add(time.Second), garden.StatusGrowing, nil, "sess-c")
	for i := 0; i < docstore.MaxLimit; i++ {
		put(fmt.Sprintf("s-1%05x", i), base.Add(time.Duration(10+i)*time.Second), garden.StatusPlanted, nil)
	}
	newest := base.Add(time.Duration(20+docstore.MaxLimit) * time.Second)
	put("s-0000b1", newest, garden.StatusPlanted, nil)
	put("s-0000b2", newest.Add(time.Second), garden.StatusGrowing, []garden.Edge{
		{Kind: garden.EdgeBlocks, To: "s-0000a2"},
		{Kind: garden.EdgeBlocks, To: "s-0000b1"},
	})
	fixture.d.ptyBackend = (&recordingDoorbell{}).backend()
	drains := observeAgentMailboxDrainsFor(t, fixture.d, "sess-c")

	resp := transition(t, fixture.d, "sess-b", "s-0000b2", garden.VerbHarvest, "done", "")
	if got := unblockedIDs(t, resp); !slices.Equal(got, []string{"s-0000a2"}) {
		t.Fatalf("with more than %d seeds the close announced %v, want only s-0000a2: "+
			"s-0000a2 sits past the snapshot page, and s-0000b1 is still blocked by s-0000a1, which sits past it too",
			docstore.MaxLimit, got)
	}
	if delivered := drains.next(); delivered != 1 {
		t.Fatalf("old dependent drain delivered %d doorbells, want 1", delivered)
	}
	assertOneSeedBell(t, fixture.d, "sess-c", "s-0000a2", gardenRingUnblocked)
}
