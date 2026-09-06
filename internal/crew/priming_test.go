package crew

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func fullPriming() Priming {
	return Priming{
		Member:        "trellis",
		HomeDir:       "/home/victor/.attn/crew/trellis",
		CharterPath:   "/home/victor/.attn/crew/trellis/CHARTER.md",
		CWD:           "/home/victor/projects/attn",
		AwarenessDirs: []string{"/home/victor/projects/pi", "/home/victor/notes"},
		Charter:       "# trellis\n\nI care about the shape of the work.",
		Handoff:       "The epic is half landed; #901 is waiting on review.",
		HandoffName:   "2026-08-13T22-20Z-trellis.md",
		OlderHandoffs: []string{"2026-08-12T21-00Z-trellis.md", "2026-08-11T20-00Z-trellis.md"},
	}
}

func TestPriming_BlockCarriesEverythingAWokenMemberNeeds(t *testing.T) {
	block := fullPriming().Block()

	for _, want := range []string{
		"You are **Trellis**",
		"The last Trellis left you what they knew",
		"Presence over persistence",
		"You are not playing a part.",
		"When your harness compacts this conversation",
		"preserve your charter, voice, and personality",
		"They are part of how you function.",
		"Compaction continues this day",
		"/home/victor/.attn/crew/trellis",
		"Begin by reading `CHARTER.md` there.",
		"/home/victor/projects/attn",
		"/home/victor/projects/pi",
		"/home/victor/notes",
		"## Your predecessor's letter (2026-08-13T22-20Z-trellis.md)",
		"#901 is waiting on review",
		"2026-08-12T21-00Z-trellis.md",
		"## Closure",
		"attn handoff -m",
		"Filing is the turning of the page",
		"a seed's handoff note belongs to the seed",
		"Someone wakes as Trellis after you",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block does not carry %q", want)
		}
	}
}

func TestPriming_ClosureCarriesHandoffFlagSemantics(t *testing.T) {
	block := fullPriming().Block()

	for _, want := range []string{
		"Plain `attn handoff` is presence-decided day turnover",
		"While the user is at the machine, a successor wakes immediately",
		"When the user asks you to sleep, file with `attn handoff --sleep`",
		"nobody wakes behind it",
		"Use `attn handoff --nap` when you explicitly want a successor",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the closure does not carry %q", want)
		}
	}
}

func TestPriming_TheCharterIsReadRatherThanInlined(t *testing.T) {
	p := fullPriming()
	p.Charter = "# trellis\n\nI care about the shape of the work."

	block := p.Block()
	if strings.Contains(block, "I care about the shape of the work.") {
		t.Error("the charter's text was inlined into the block")
	}
	if !strings.Contains(block, "Begin by reading `CHARTER.md` there.") {
		t.Error("the block does not send the member to its charter")
	}
}

func TestPriming_TheNameIsCapitalizedOnlyInProse(t *testing.T) {
	block := fullPriming().Block()

	if strings.Contains(block, "You are **trellis**") {
		t.Error("the member is addressed by its id rather than by its name")
	}
	if !strings.Contains(block, "`/home/victor/.attn/crew/trellis`") {
		t.Error("the home path was capitalized along with the name")
	}
}

func TestPriming_AnUnboundSessionGetsNoBlock(t *testing.T) {
	if got := (Priming{HomeDir: "/somewhere", Charter: "# nobody"}).Block(); got != "" {
		t.Fatalf("an unbound session was primed with %q", got)
	}
}

func TestPriming_AFirstDayNamesWhatIsMissing(t *testing.T) {
	block := Priming{Member: "sable", HomeDir: "/homes/sable"}.Block()

	for _, want := range []string{
		"this is your first day",
		"a self, not a job description",
		"/homes/sable/CHARTER.md",
		"No letter is waiting for you",
		"/homes/sable/handoffs",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("a first day does not say %q", want)
		}
	}
}

func TestPriming_AFiledLetterAtTheLimitIsInlinedWhole(t *testing.T) {
	p := fullPriming()
	p.Handoff = strings.Repeat("x", MaxHandoffBytes)
	block := p.Block()

	if !strings.Contains(block, p.Handoff) {
		t.Fatal("a letter accepted by FileHandoff was not inlined whole")
	}
	if strings.Contains(block, "Before responding to the user, read the whole file") {
		t.Fatal("a letter within the filing limit was treated as oversize")
	}
}

func TestPriming_AHandEditedOversizeLetterRequiresAFullRead(t *testing.T) {
	p := Priming{Member: "keel", HomeDir: "/homes/keel", HandoffName: "2026-08-13T22-20Z-keel.md"}
	p.Handoff = strings.Repeat("日", handoffInlineLimit)

	block := p.Block()
	if !utf8.ValidString(block) {
		t.Fatal("the cut split a rune: the block is not valid UTF-8")
	}
	for _, want := range []string{"hand-edited letter", "Before responding to the user, read the whole file", p.HandoffName} {
		if !strings.Contains(block, want) {
			t.Errorf("the oversize letter guidance does not contain %q", want)
		}
	}
}

func TestPriming_HandoffNamesSortFreshestFirst(t *testing.T) {
	names := []string{
		"2026-08-11T20-00Z-keel.md",
		"2026-08-13T22-10Z-keel.md",
		"2026-08-12T21-00Z-keel.md",
	}
	SortHandoffNames(names)
	if names[0] != "2026-08-13T22-10Z-keel.md" || names[2] != "2026-08-11T20-00Z-keel.md" {
		t.Fatalf("sorted to %v, want freshest first", names)
	}
}

func holding(seeds int) Priming {
	p := fullPriming()
	p.GardenRead = true
	for i := range seeds {
		p.Held = append(p.Held, HeldSeed{
			ID:      fmt.Sprintf("s-held%02d", i),
			Slug:    fmt.Sprintf("held-seed-%d", i),
			Title:   fmt.Sprintf("Held seed %d", i),
			Handoff: fmt.Sprintf("Where seed %d stands.", i),
		})
	}
	p.HeldTotal = seeds
	p.Plots = []PlotReady{{ID: "s-plot01", Slug: "finish-garden", Title: "Finish the garden", Ready: 4}}
	return p
}

func TestPriming_TheGardenSectionCarriesEachHeldSeedAndItsHandoff(t *testing.T) {
	p := holding(2)
	p.Held[1].Handoff = ""
	block := p.Block()

	for _, want := range []string{
		"## What you hold in the garden",
		"Your claim on a seed never expires",
		"`s-held00` held-seed-0 — Held seed 0",
		"Freshest handoff: Where seed 0 stands.",
		"`s-held01` held-seed-1 — Held seed 1",
		"No handoff note yet.",
		"Ready beside them",
		"- `s-plot01` finish-garden — 4 ready",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the garden section does not carry %q", want)
		}
	}
	if strings.Contains(block, "You hold no seeds") {
		t.Error("a member holding two seeds was told it holds none")
	}
}

func TestPriming_AMemberHoldingNothingGetsOneQuietLine(t *testing.T) {
	p := fullPriming()
	p.GardenRead = true

	block := p.Block()
	if !strings.Contains(block, "You hold no seeds in the garden. `attn seed ready --all` shows what is free to pick up.") {
		t.Error("an empty garden section does not say so or where to look")
	}
	if strings.Contains(block, "## What you hold in the garden") {
		t.Error("an empty garden section still opened a heading")
	}
}

func TestPriming_AGardenThatCouldNotBeReadIsNoSectionAtAll(t *testing.T) {
	block := fullPriming().Block()
	for _, unwanted := range []string{"## What you hold in the garden", "You hold no seeds in the garden"} {
		if strings.Contains(block, unwanted) {
			t.Errorf("an unread garden still told the member %q", unwanted)
		}
	}
}

func TestPriming_PastTheHeldTripwireTheBlockNamesTheLimitAndTheAsk(t *testing.T) {
	p := holding(MaxHeldSeeds)
	p.HeldTotal = MaxHeldSeeds + 4

	block := p.Block()
	want := fmt.Sprintf("You hold %d seeds and this block lists the %d you claimed most recently; `attn seed ls --flat` has them all.", MaxHeldSeeds+4, MaxHeldSeeds)
	if !strings.Contains(block, want) {
		t.Errorf("the cut list does not say %q", want)
	}
}

func TestPriming_AListThatFitsSaysNothingAboutTheTripwire(t *testing.T) {
	if block := holding(3).Block(); strings.Contains(block, "attn seed ls --flat") {
		t.Error("a member holding three seeds was told its list was cut")
	}
}

func TestPriming_AnOversizeHandoffNoteIsTrimmedWithTheWayToTheWholeNote(t *testing.T) {
	p := holding(1)
	p.Held[0].Handoff = strings.Repeat("日", MaxHeldHandoffBytes)

	block := p.Block()
	if !utf8.ValidString(block) {
		t.Fatal("the cut split a rune: the block is not valid UTF-8")
	}
	want := fmt.Sprintf("[Trimmed at %d bytes of %d; `attn seed notes s-held00` has the whole note.]", MaxHeldHandoffBytes, MaxHeldHandoffBytes*3)
	if !strings.Contains(block, want) {
		t.Errorf("the trimmed note does not say %q", want)
	}
}

func TestPriming_AHandoffNoteAtTheBudgetIsCarriedWhole(t *testing.T) {
	p := holding(1)
	p.Held[0].Handoff = strings.Repeat("x", MaxHeldHandoffBytes)

	block := p.Block()
	if !strings.Contains(block, p.Held[0].Handoff) {
		t.Fatal("a note within the budget was not carried whole")
	}
	if strings.Contains(block, "Trimmed at") {
		t.Fatal("a note within the budget was treated as oversize")
	}
}

func TestPriming_AnUnboundSessionIsToldNothingAboutTheGarden(t *testing.T) {
	p := holding(2)
	p.Member = ""
	if got := p.GardenSection(); got != "" {
		t.Fatalf("an unbound session was primed with %q", got)
	}
}
