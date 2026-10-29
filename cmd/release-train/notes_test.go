package main

import (
	"errors"
	"strings"
	"testing"
)

func (repo *testRepository) commitMessage(paragraphs ...string) string {
	repo.t.Helper()
	repo.git("add", "-A")
	args := []string{"commit", "-q"}
	for _, paragraph := range paragraphs {
		args = append(args, "-m", paragraph)
	}
	repo.git(args...)
	return repo.git("rev-parse", "HEAD")
}

func TestNotesGroupTheCommitsSinceTheLastReleaseByTheirSeeds(t *testing.T) {
	repo := newTestRepository(t)
	repo.write("shipped.txt", "already released\n")
	previousMain := repo.commit("feat: shipped in the previous release")
	repo.writeManifest(candidateManifest{Version: "0.11.1", MainSHA: previousMain})
	repo.commit("chore(release): prepare v0.11.1")

	repo.write("band.txt", "one\n")
	repo.commitMessage("feat(queue): first slice of the band", "Attn-Session: s1\nAttn-Seed: seed-band")
	repo.write("band.txt", "two\n")
	repo.commitMessage("feat(queue): second slice of the band", "Wires the band into the queue.", "Attn-Session: s2\nAttn-Seed: seed-band\nAttn-Seed: seed-palette")
	repo.write("gone.txt", "fix\n")
	repo.commitMessage("fix(palette): keep the selection", "Attn-Seed: seed-gone")
	repo.write("changelog.d/tidy.yaml", "kind: fixed\narea: queue\nchange: The queue keeps its order.\n")
	repo.commitMessage("fix(queue): keep the order")

	seeds := map[string]seedRecord{}
	band := seedRecord{}
	band.Seed.Title, band.Seed.Body, band.Seed.Status = "Queue band", "Show who is waiting.", "harvested"
	band.Notes = append(band.Notes, struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	}{Kind: "note", Body: "Users asked for it."})
	seeds["seed-band"] = band
	palette := seedRecord{}
	palette.Seed.Title, palette.Seed.Status = "Palette", "growing"
	seeds["seed-palette"] = palette
	lookup := func(id string) (seedRecord, error) {
		record, ok := seeds[id]
		if !ok {
			return seedRecord{}, errors.New("no seed " + id)
		}
		return record, nil
	}

	compiled, err := releaseRangeAt(repo.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if compiled.from != previousMain {
		t.Fatalf("range starts at %s, want the previous release's main %s", compiled.from, previousMain)
	}
	rendered, err := renderNotes(repo.root, compiled, lookup)
	if err != nil {
		t.Fatal(err)
	}

	groups := strings.Split(rendered, "\n=== ")
	if !strings.HasPrefix(groups[0], "4 commits since the last release") {
		t.Fatalf("header = %q", groups[0])
	}
	want := []struct {
		heading string
		holds   []string
	}{
		{"seed seed-band: Queue band [harvested]", []string{"Show who is waiting.", "--- note: Users asked for it.", "first slice of the band", "second slice of the band", "Wires the band into the queue."}},
		{"seed seed-palette: Palette [growing]", []string{"second slice of the band"}},
		{"seed seed-gone: unknown (no seed seed-gone)", []string{"fix(palette): keep the selection"}},
		{"commits without a seed", []string{"fix(queue): keep the order", "release note the author wrote (changelog.d/tidy.yaml)", "The queue keeps its order."}},
	}
	if len(groups) != len(want)+1 {
		t.Fatalf("got %d groups, want %d:\n%s", len(groups)-1, len(want), rendered)
	}
	for i, group := range want {
		got := groups[i+1]
		if !strings.HasPrefix(got, group.heading) {
			t.Errorf("group %d starts %q, want %q", i, firstLine(got), group.heading)
		}
		for _, text := range group.holds {
			if !strings.Contains(got, text) {
				t.Errorf("group %q lacks %q:\n%s", group.heading, text, got)
			}
		}
	}
	for _, released := range []string{"shipped in the previous release", "prepare v0.11.1"} {
		if strings.Contains(rendered, released) {
			t.Errorf("notes include %q, which is not part of this release", released)
		}
	}
}
