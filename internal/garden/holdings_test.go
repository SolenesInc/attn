package garden

import (
	"strings"
	"testing"
)

func held(seeds []Seed) string { return strings.Join(ids(seeds), ",") }

func seedHeldBy(id, member, status, changedAt string, edges ...Edge) Seed {
	return Seed{ID: id, Title: id, Status: status, TenderMember: member, StateChangedAt: changedAt, Edges: edges}
}

func gardenWithHoldings() []Seed {
	return []Seed{
		{ID: "s-plot01", Title: "Finish the garden", StepSlug: "finish-garden", Status: StatusPlanted},
		seedHeldBy("s-held01", "trellis", StatusGrowing, "2026-09-01T10:00:00Z", partOf("s-plot01")),
		seedHeldBy("s-held02", "trellis", StatusGrowing, "2026-09-03T10:00:00Z", partOf("s-plot01")),
		seedHeldBy("s-held03", "trellis", StatusGrowing, "2026-09-02T10:00:00Z"),
		seedHeldBy("s-other1", "alder", StatusGrowing, "2026-09-04T10:00:00Z"),
		seedHeldBy("s-done01", "trellis", StatusHarvested, "2026-09-05T10:00:00Z"),
		{ID: "s-free01", Title: "nobody's", Status: StatusPlanted, Edges: []Edge{partOf("s-plot01")}},
	}
}

func TestHeld_IsTheMembersOpenClaimsFreshestFirst(t *testing.T) {
	if got := held(Held(gardenWithHoldings(), "trellis")); got != "s-held02,s-held03,s-held01" {
		t.Fatalf("trellis holds %q, want its three open claims freshest first", got)
	}
	if got := held(Held(gardenWithHoldings(), "alder")); got != "s-other1" {
		t.Fatalf("alder holds %q, want only its own claim", got)
	}
}

func TestHeld_NobodyAndNoClaimsHoldNothing(t *testing.T) {
	if got := Held(gardenWithHoldings(), "  "); len(got) != 0 {
		t.Fatalf("an unnamed member holds %q", held(got))
	}
	if got := Held(gardenWithHoldings(), "keel"); len(got) != 0 {
		t.Fatalf("keel holds %q without ever claiming a seed", held(got))
	}
}

func TestHeld_TheClaimIsMatchedWithoutCase(t *testing.T) {
	if got := held(Held(gardenWithHoldings(), "Trellis")); got != "s-held02,s-held03,s-held01" {
		t.Fatalf("Trellis holds %q, want the same seeds trellis holds", got)
	}
}

func TestPlotsOf_IsEachPlotOnceInTheOrderTheSeedsArrive(t *testing.T) {
	seeds := gardenWithHoldings()
	plots := PlotsOf(seeds, Held(seeds, "trellis"))
	if got := held(plots); got != "s-plot01" {
		t.Fatalf("trellis reports to %q, want the one plot its held seeds share", got)
	}
	if plots[0].StepSlug != "finish-garden" {
		t.Fatalf("the plot came back as %+v, want the crown seed itself", plots[0])
	}
}

func TestPlotsOf_APlotThatIsNoLongerPlantedIsNotNamed(t *testing.T) {
	seeds := []Seed{seedHeldBy("s-held01", "trellis", StatusGrowing, "", partOf("s-gone00"))}
	if got := PlotsOf(seeds, Held(seeds, "trellis")); len(got) != 0 {
		t.Fatalf("a seed under a missing plot named %q", held(got))
	}
}
