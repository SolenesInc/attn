package crew

import (
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
		"While Victor is at the machine, a successor wakes immediately",
		"When Victor asks you to sleep, file with `attn handoff --sleep`",
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

func TestPriming_AnOversizeFileIsCutWithWhereTheRestIs(t *testing.T) {
	p := fullPriming()
	p.Handoff = strings.Repeat("letter. ", HandoffLimit/4)
	block := p.Block()

	if len(block) > HandoffLimit+8000 {
		t.Fatalf("block is %d bytes; the limit did not bound it", len(block))
	}
	if !strings.Contains(block, "[cut at ") || !strings.Contains(block, p.HandoffName) {
		t.Error("the cut does not say where the whole file is")
	}
}

func TestPriming_ACutLandsOnARuneBoundary(t *testing.T) {
	p := Priming{Member: "keel", HomeDir: "/homes/keel", HandoffName: "2026-08-13T22-20Z-keel.md"}
	p.Handoff = strings.Repeat("日", HandoffLimit)

	block := p.Block()
	if !utf8.ValidString(block) {
		t.Fatal("the cut split a rune: the block is not valid UTF-8")
	}
	if !strings.Contains(block, "[cut at ") {
		t.Fatal("an oversize letter was not cut")
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
