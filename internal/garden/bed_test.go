package garden

import (
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestBedValidationAndLookup(t *testing.T) {
	if _, err := ValidateBedName("  "); err == nil {
		t.Fatal("expected empty bed name to be refused")
	}
	for _, reserved := range []string{"All", "unassigned", "AUTOMATIONS", " Unassigned "} {
		if _, err := ValidateBedName(reserved); err == nil {
			t.Fatalf("expected reserved bed name %q to be refused", reserved)
		}
	}
	if _, err := ValidateBedName("line1\nline2"); err == nil {
		t.Fatal("expected multiline bed name to be refused")
	}
	if _, err := ValidateBedName(strings.Repeat("a", MaxBedNameChars+1)); err == nil {
		t.Fatal("expected overly long bed name to be refused")
	}
	name, err := ValidateBedName("  Payments API  ")
	if err != nil || name != "Payments API" {
		t.Fatalf("ValidateBedName = (%q, %v), want (Payments API, nil)", name, err)
	}
	if got := BedSlug(name); got != "payments-api" {
		t.Fatalf("BedSlug = %q, want payments-api", got)
	}

	if _, err := ValidateBedNote("a\nb"); err == nil {
		t.Fatal("expected multiline note to be refused")
	}
	if _, err := ValidateBedNote(strings.Repeat("x", MaxBedNoteChars+1)); err == nil {
		t.Fatal("expected overly long note to be refused")
	}

	for _, invalid := range []string{"", "two\nlines"} {
		if _, err := ValidateBedRepo(invalid); err == nil {
			t.Fatalf("invalid repo %q accepted", invalid)
		}
	}
	if repo, err := ValidateBedRepo(" Payments "); err != nil || repo != "Payments" {
		t.Fatalf("ValidateBedRepo = (%q, %v), want Payments", repo, err)
	}

	beds := []Bed{
		{ID: "b-222222", Name: "Payments API", Slug: "payments-api"},
		{ID: "b-333333", Name: "Core", Slug: "core"},
	}
	if got, ok := FindBed(beds, "b-222222"); !ok || got.ID != "b-222222" {
		t.Fatalf("FindBed by ID = (%+v, %v)", got, ok)
	}
	if got, ok := FindBed(beds, "payments api"); !ok || got.ID != "b-222222" {
		t.Fatalf("FindBed by Name = (%+v, %v)", got, ok)
	}
	if got, ok := FindBed(beds, "payments-api"); !ok || got.ID != "b-222222" {
		t.Fatalf("FindBed by Slug = (%+v, %v)", got, ok)
	}
	if _, ok := FindBed(beds, "unknown"); ok {
		t.Fatal("expected unknown bed lookup to return false")
	}

	if _, err := ValidatePulse(""); err == nil {
		t.Fatal("expected empty pulse to be refused")
	}
	if _, err := ValidatePulse("line1\nline2"); err == nil {
		t.Fatal("expected multiline pulse to be refused")
	}
	if _, err := ValidatePulse(strings.Repeat("p", MaxPulseChars+1)); err == nil {
		t.Fatal("expected overly long pulse to be refused")
	}
	if got, err := ValidatePulse("  Tests pass; waiting on review  "); err != nil || got != "Tests pass; waiting on review" {
		t.Fatalf("ValidatePulse = (%q, %v)", got, err)
	}
}

func TestMatchBedForRepoExactPathBeatsBasenameProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir := rapid.SampledFrom([]string{"/work/org", "/Users/dev/code", "/srv/repos"}).Draw(t, "dir")
		repo := rapid.SampledFrom([]string{"attn", "payments", "web-app", "billing"}).Draw(t, "repo")
		full := filepath.Join(dir, repo)
		other := filepath.Join("/other", repo)

		bedExact := Bed{ID: "b-999999", Name: "Exact", Slug: "exact", BedRepositories: BedRepositories{Paths: []RepoPath{{Path: full}}}}
		bedBase := Bed{ID: "b-222222", Name: "Base", Slug: "base", BedRepositories: BedRepositories{Names: []string{strings.ToUpper(repo)}}}
		bedNoise := Bed{ID: "b-333333", Name: "Noise", Slug: "noise", BedRepositories: BedRepositories{Names: []string{"unrelated"}, Paths: []RepoPath{{Path: "/tmp/unrelated"}}}}

		orderA := []Bed{bedBase, bedNoise, bedExact}
		orderB := []Bed{bedExact, bedBase, bedNoise}

		match := func(beds []Bed, checkout string) (Bed, bool) {
			path := filepath.Clean(checkout)
			return MatchBedForRepo(beds, path, func(binding RepoPath) bool {
				return path == binding.Path || strings.HasPrefix(path, binding.Path+"/")
			})
		}
		gotA, okA := match(orderA, full+"/")
		gotB, okB := match(orderB, full)
		if !okA || !okB || gotA.ID != bedExact.ID || gotB.ID != bedExact.ID {
			t.Fatalf("exact path match failed: A=(%q,%v) B=(%q,%v) want %q", gotA.ID, okA, gotB.ID, okB, bedExact.ID)
		}
		// Subdirectory inside a bound repo path matches its parent repo, while a sibling prefix does not.
		gotSub, okSub := match(orderA, filepath.Join(full, "pkg", "sub"))
		if !okSub || gotSub.ID != bedExact.ID {
			t.Fatalf("subdir path match failed: got (%q,%v) want %q", gotSub.ID, okSub, bedExact.ID)
		}
		if gotSib, okSib := match(orderA, full+"-sibling"); okSib {
			t.Fatalf("sibling prefix unexpectedly matched bed %q", gotSib.ID)
		}

		gotFallback, okFallback := match(orderA, other)
		if !okFallback || gotFallback.ID != bedBase.ID {
			t.Fatalf("basename fallback failed: got (%q,%v) want %q", gotFallback.ID, okFallback, bedBase.ID)
		}
	})
}

func TestEffectiveBedIDInheritsFromRootPlotProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(1, 6).Draw(t, "depth")
		rootBed := rapid.SampledFrom([]string{"", "b-222222", "b-333333"}).Draw(t, "rootBed")

		seeds := make([]Seed, depth)
		for i := range depth {
			id := "s-00000" + string(rune('a'+i))
			seed := Seed{ID: id, Title: id, Status: StatusPlanted}
			if i == 0 {
				seed.BedID = rootBed
			} else {
				seed.Edges = []Edge{{Kind: EdgePartOf, To: seeds[i-1].ID}}
				seed.BedID = "b-stale0"
			}
			seeds[i] = seed
		}
		plots := NewPlotTree(seeds)
		for _, seed := range seeds {
			if got := EffectiveBedID(seed, plots); got != rootBed {
				t.Fatalf("EffectiveBedID(%s) = %q, want root bed %q", seed.ID, got, rootBed)
			}
		}
	})
}
