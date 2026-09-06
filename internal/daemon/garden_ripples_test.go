package daemon

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// A close ripples through blocks edges, so the fixture keeps a real plot around
// the pipe: the announcement must name the dependents and nothing else.
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

func TestSeedRipples_HarvestNamesTheSeedItFreed(t *testing.T) {
	fixture := newRippleGarden(t)
	move(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbTend, "", "")

	resp := transition(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	if got := unblockedIDs(t, resp); !slices.Equal(got, []string{fixture.dependent.ID}) {
		t.Fatalf("harvest announced %v, want the one seed it freed (%s)", got, fixture.dependent.ID)
	}
}

func TestSeedRipples_HarvestNamesEverySeedItFreed(t *testing.T) {
	fixture := newRippleGarden(t)
	second := plantUnder(t, fixture.seededNudgeGarden, "paint the wall")
	mustLink(t, fixture.d, fixture.blocker.ID, garden.EdgeBlocks, second.ID)

	resp := transition(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	want := []string{fixture.dependent.ID, second.ID}
	slices.Sort(want)
	if got := unblockedIDs(t, resp); !slices.Equal(got, want) {
		t.Fatalf("harvest announced %v, want both freed seeds %v", got, want)
	}
}

func TestSeedRipples_ASecondBlockerKeepsTheDependentQuiet(t *testing.T) {
	fixture := newRippleGarden(t)
	other := plantUnder(t, fixture.seededNudgeGarden, "pour the slab")
	mustLink(t, fixture.d, other.ID, garden.EdgeBlocks, fixture.dependent.ID)

	held := transition(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	if got := unblockedIDs(t, held); len(got) != 0 {
		t.Fatalf("a dependent %s still blocks was announced: %v", other.ID, got)
	}
	freed := transition(t, fixture.d, "sess-b", other.ID, garden.VerbHarvest, "slab poured", "")
	if got := unblockedIDs(t, freed); !slices.Equal(got, []string{fixture.dependent.ID}) {
		t.Fatalf("the last blocker announced %v, want %s", got, fixture.dependent.ID)
	}
}

func TestSeedRipples_ACloseThatFreedNothingIsQuiet(t *testing.T) {
	fixture := newRippleGarden(t)
	resp := transition(t, fixture.d, "sess-b", fixture.dependent.ID, garden.VerbWither, "not wanted", "")
	if got := unblockedIDs(t, resp); len(got) != 0 {
		t.Fatalf("closing a seed that blocked nobody announced %v", got)
	}
}

func TestSeedRipples_WitherFreesWhatHarvestWould(t *testing.T) {
	fixture := newRippleGarden(t)
	resp := transition(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbWither, "the pipe is somebody else's", "")
	if got := unblockedIDs(t, resp); !slices.Equal(got, []string{fixture.dependent.ID}) {
		t.Fatalf("wither announced %v, want %s", got, fixture.dependent.ID)
	}
}

func TestSeedRipples_TheDoorbellRingsWhoeverHoldsTheFreedSeed(t *testing.T) {
	fixture := newRippleGarden(t)
	move(t, fixture.d, "sess-c", fixture.dependent.ID, garden.VerbTend, "", "")

	move(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	assertOneSeedBell(t, fixture.d, "sess-c", fixture.dependent.ID, gardenRingUnblocked)
}

func TestSeedRipples_AFreedSeedNobodyHoldsRingsNobody(t *testing.T) {
	fixture := newRippleGarden(t)
	move(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")

	for _, session := range []string{"sess-a", "sess-b", "sess-c", "sess-d"} {
		if queued := queuedSeedBells(t, fixture.d, session); len(queued) != 0 {
			t.Fatalf("freeing an untended seed rang %s: %q", session, queued)
		}
	}
}

func TestSeedRipples_TheHarvesterIsNotRungForASeedTheyHold(t *testing.T) {
	fixture := newRippleGarden(t)
	move(t, fixture.d, "sess-b", fixture.dependent.ID, garden.VerbTend, "", "")

	move(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("the session that closed the blocker rang itself: %q", queued)
	}
}

func TestSeedRipples_AMemberIsRungAtTheSessionItsBindingHolds(t *testing.T) {
	fixture := newRippleGarden(t)
	writeCrewHomes(t, fixture.d.dataRoot)
	fixture.d.ensureCrewCollections()
	fixture.d.importCrewHomes()
	if _, err := fixture.d.claimCrewBinding("trellis", "sess-d"); err != nil {
		t.Fatalf("bind trellis: %v", err)
	}
	move(t, fixture.d, "", fixture.dependent.ID, garden.VerbTend, "", "trellis")

	move(t, fixture.d, "sess-b", fixture.blocker.ID, garden.VerbHarvest, "pipe laid", "")
	assertOneSeedBell(t, fixture.d, "sess-d", fixture.dependent.ID, gardenRingUnblocked)
}

func TestSeedRipples_AMergedPullRequestRipplesLikeAnyOtherClose(t *testing.T) {
	fixture := newRippleGarden(t)
	move(t, fixture.d, "sess-c", fixture.dependent.ID, garden.VerbTend, "", "")
	rec := recordPullRequest(t, fixture.d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	if resp := armWhenMerged(t, fixture.d, "sess-a", fixture.blocker.ID, rec.URL); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	settlePullRequest(t, fixture.d, rec.PRID, sessionPullRequestMerged, "Lay the pipe")

	armed, _, err := fixture.d.readSeed(fixture.blocker.ID)
	if err != nil {
		t.Fatalf("read %s: %v", fixture.blocker.ID, err)
	}
	merged, ok := sessionPullRequestByID(fixture.d.store.ListSessionPullRequests("sess-a"), rec.PRID)
	if !ok {
		t.Fatalf("the merged row went missing")
	}
	if _, _, err := fixture.d.fulfilHarvestWhen(armed, merged, nil); err != nil {
		t.Fatalf("fulfil: %v", err)
	}
	assertOneSeedBell(t, fixture.d, "sess-c", fixture.dependent.ID, gardenRingUnblocked)
}

func TestSeedRipples_ArmingAnAlreadyMergedPullRequestAnnouncesTheRipple(t *testing.T) {
	fixture := newRippleGarden(t)
	rec := recordPullRequest(t, fixture.d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	settlePullRequest(t, fixture.d, rec.PRID, sessionPullRequestMerged, "Lay the pipe")

	resp := armWhenMerged(t, fixture.d, "sess-a", fixture.blocker.ID, rec.URL)
	if got := unblockedIDs(t, resp); !slices.Equal(got, []string{fixture.dependent.ID}) {
		t.Fatalf("an on-the-spot harvest announced %v, want %s", got, fixture.dependent.ID)
	}
}

func TestSeedRipples_ArmingAlonePromisesNothingYet(t *testing.T) {
	fixture := newRippleGarden(t)
	rec := recordPullRequest(t, fixture.d, "sess-a", "https://github.com/victorarias/attn/pull/71")

	resp := armWhenMerged(t, fixture.d, "sess-a", fixture.blocker.ID, rec.URL)
	if got := unblockedIDs(t, resp); len(got) != 0 {
		t.Fatalf("arming a seed that has not closed announced %v", got)
	}
}
