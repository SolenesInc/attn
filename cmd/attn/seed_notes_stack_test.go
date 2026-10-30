package main_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSeedNotesNarrowsToABookmarkAndAKind(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	seed := plant(t, s, "A seed with every kind of entry", "-m", "# Work\n")
	plot := plant(t, s, "A plot above it")
	seedAs(t, s, "", "link", seed.ID, "part-of", plot.ID)

	writers := map[string]func(){
		garden.NoteKindNote:    func() { seedAs(t, s, "", "note", seed.ID, "-m", "a plain note") },
		garden.NoteKindHandoff: func() { seedAs(t, s, "", "note", seed.ID, "-m", "for whoever is next", "--handoff") },
		garden.NoteKindAttach:  func() { seedAs(t, s, "", "attach", seed.ID, "--url", "https://example.com/spec") },
		garden.NoteKindDetach:  func() { seedAs(t, s, "", "detach", seed.ID, "--url", "https://example.com/spec") },
		garden.NoteKindCheck:   func() { seedAs(t, s, "", "check", "add", seed.ID, "C1", "the entries narrow") },
		garden.NoteKindEdit: func() {
			exported := seedAs(t, s, "", "export", seed.ID, "--out", "-")
			s.Run(testworld.Invocation{Args: []string{"seed", "edit", seed.ID, "-m", "-"}, Stdin: exported + "More.\n"})
		},
	}
	for _, kind := range garden.EntryKinds {
		write, ok := writers[kind]
		if !ok {
			t.Fatalf("no command here writes a %s entry; add one so --kind %s is checked", kind, kind)
		}
		write()
	}

	for _, kind := range garden.EntryKinds {
		var narrowed protocol.SeedNotesResult
		s.Attn("seed", "notes", seed.ID, "--kind", kind, "--json").JSON(t, &narrowed)
		if len(narrowed.Notes) == 0 || narrowed.Total != len(narrowed.Notes) {
			t.Errorf("notes --kind %s returned %d of %d entries, want every one of that kind", kind, len(narrowed.Notes), narrowed.Total)
		}
		for _, entry := range narrowed.Notes {
			if entry.Kind != kind {
				t.Errorf("notes --kind %s returned a %s entry: %q", kind, entry.Kind, entry.Body)
			}
		}
	}

	var before protocol.SeedNotesResult
	s.Attn("seed", "notes", plot.ID, "--json").JSON(t, &before)
	bookmark := strconv.Itoa(before.Bookmark)
	seedAs(t, s, "", "note", seed.ID, "-m", "after the bookmark")
	seedAs(t, s, "", "note", seed.ID, "-m", "a handoff after the bookmark", "--handoff")

	var since protocol.SeedNotesResult
	s.Attn("seed", "notes", plot.ID, "--since", bookmark, "--json").JSON(t, &since)
	if got := entryBodies(since.Notes); got != "a handoff after the bookmark|after the bookmark" {
		t.Errorf("notes --since %s on the plot returned %q, want the two entries saved after it, newest first", bookmark, got)
	}
	var handoffs protocol.SeedNotesResult
	s.Attn("seed", "notes", plot.ID, "--since", bookmark, "--kind", "handoff", "--json").JSON(t, &handoffs)
	if got := entryBodies(handoffs.Notes); got != "a handoff after the bookmark" {
		t.Errorf("notes --since %s --kind handoff returned %q, want only the new handoff", bookmark, got)
	}

	printed := seedAs(t, s, "", "notes", plot.ID, "--since", bookmark)
	requireLines(t, "notes --since", printed,
		"since "+bookmark+": 2 new entries",
		seed.ID+"  handoff  a handoff after the bookmark",
		seed.ID+"  note  after the bookmark",
		"bookmark  attn seed notes "+plot.ID+" --since "+strconv.Itoa(since.Bookmark))
	requireLines(t, "a full notes read", seedAs(t, s, "", "notes", seed.ID, "--kind", "check"),
		"bookmark  attn seed notes "+seed.ID+" --kind check --since ")
	requireLines(t, "a repeated since-read", seedAs(t, s, "", "notes", plot.ID, "--since", strconv.Itoa(since.Bookmark)),
		"since "+strconv.Itoa(since.Bookmark)+": nothing new")

	refused := s.Run(testworld.Invocation{Args: []string{"seed", "notes", seed.ID, "--kind", "gossip"}})
	if refused.Code == 0 {
		t.Fatalf("notes --kind gossip succeeded: %s", refused.Stdout)
	}
	for _, kind := range garden.EntryKinds {
		if !strings.Contains(refused.Stderr, kind) {
			t.Errorf("the refusal of --kind gossip does not list %s: %s", kind, refused.Stderr)
		}
	}
}

func entryBodies(entries []protocol.SeedNote) string {
	bodies := make([]string, 0, len(entries))
	for _, entry := range entries {
		bodies = append(bodies, entry.Body)
	}
	return strings.Join(bodies, "|")
}
