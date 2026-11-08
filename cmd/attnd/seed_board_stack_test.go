package main_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPlotsShowIsABoardOfItsOpenChildren(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "alder")
	s.Start()
	plot := plantPlot(t, s, `{"title": "Fence the garden", "children": [
		{"title": "Dig the postholes"},
		{"title": "Set the posts", "checks": [{"id": "C1", "text": "every post stands plumb"}]},
		{"title": "Buy the timber"},
		{"title": "Paint a mural"},
		{"title": "Hang the gate"}]}`)
	dig, set, buy, mural, gate := plot.Children[0], plot.Children[1], plot.Children[2], plot.Children[3], plot.Children[4]
	hinge := plant(t, s, "Fit the hinges", "--part-of", gate.ID)
	seedAs(t, s, "", "link", dig.ID, "blocks", set.ID)
	seedAs(t, s, "", "tend", dig.ID, "--for", "alder")
	seedAs(t, s, "", "note", dig.ID, "-m", "four holes dug\nthe rest tomorrow")
	seedAs(t, s, "", "harvest", buy.ID, "-m", "timber delivered")
	seedAs(t, s, "", "wither", mural.ID, "-m", "no mural this year")

	shown := seedAs(t, s, "", "show", plot.Crown.ID)
	board := shown[strings.Index(shown, "\nboard  "):]
	requireLines(t, "the board", board, "\nboard  3 open\n",
		"\n  and 1 harvested, 1 withered — attn seed ls --plot "+plot.Crown.ID+"\n")
	rows := map[string]*regexp.Regexp{
		dig.ID:  regexp.MustCompile(`(?m)^  ` + dig.ID + `  +` + dig.StepSlug + `  +growing  +Alder  +-  +-  +\S+ \S+  the user  four holes dug$`),
		set.ID:  regexp.MustCompile(`(?m)^  ` + set.ID + `  +` + set.StepSlug + `  +planted  +-  +waits for ` + dig.ID + `  +checks ✓0/1  +`),
		gate.ID: regexp.MustCompile(`(?m)^  ` + gate.ID + `  +` + gate.StepSlug + ` +planted · 0/1 done  +-  +-  +-  +`),
	}
	for id, row := range rows {
		if !row.MatchString(board) {
			t.Errorf("the board has no row for %s matching %s:\n%s", id, row, board)
		}
	}
	for _, closed := range []string{buy.ID, mural.ID, hinge.ID} {
		if regexp.MustCompile(`(?m)^\s*(\S+\s+)?` + closed + `\s`).MatchString(shown) {
			t.Errorf("show of the plot gives %s, which is closed or not a direct child, a row:\n%s", closed, shown)
		}
	}
	if strings.Contains(shown, "has-part") {
		t.Errorf("show lists the children as edges as well as on the board:\n%s", shown)
	}

	result := showSeed(t, s, "", plot.Crown.ID)
	if got := result.ClosedChildren; got == nil || got.Harvested != 1 || got.Withered != 1 {
		t.Errorf("closed_children is %+v, want 1 harvested and 1 withered", got)
	}
	byID := map[string]protocol.SeedBoardRow{}
	for _, row := range result.Board {
		byID[row.Seed.ID] = row
	}
	if len(byID) != 3 || byID[set.ID].WaitsFor[0] != dig.ID || byID[dig.ID].Newest == nil || byID[gate.ID].Seed.PlotProgress == nil {
		t.Errorf("the board's JSON rows are %+v, want dig, set (waiting for dig) and gate (a plot) with dig's newest entry", result.Board)
	}

	listed := seedAs(t, s, "", "ls", "--plot", plot.Crown.ID)
	for _, child := range plot.Children {
		if _, line := lineOf(t, listed, child.ID); !strings.Contains(line, child.StepSlug) {
			t.Errorf("ls --plot lists %s without its slug: %q", child.ID, line)
		}
	}
	requireLines(t, "ls --plot", listed, "  harvested  ", "  withered  ")
	if strings.Contains(listed, hinge.ID) || strings.Contains(listed, plot.Crown.ID) {
		t.Errorf("ls --plot lists more than the plot's direct children:\n%s", listed)
	}

	leaf := seedAs(t, s, "", "show", hinge.ID)
	if strings.Contains(leaf, "\nboard  ") {
		t.Errorf("a seed without children shows a board:\n%s", leaf)
	}
	requireLines(t, "ls --plot of a leaf", seedAs(t, s, "", "ls", "--plot", hinge.ID), hinge.ID+" has no children")
}

func requireOrder(t *testing.T, what, text string, want ...string) {
	t.Helper()
	rest := text
	for _, part := range want {
		at := strings.Index(rest, part)
		if at < 0 {
			t.Errorf("%s does not say %q after the parts before it:\n%s", what, part, text)
			return
		}
		rest = rest[at+len(part):]
	}
}
