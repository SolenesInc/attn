package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func searchResult(hits int, matched int, limit int) *protocol.SeedSearchResult {
	result := &protocol.SeedSearchResult{Matched: matched, Limit: limit, Searched: 477}
	for range hits {
		result.Hits = append(result.Hits, protocol.SeedSearchHit{
			Seed: protocol.Seed{
				ID: "s-7k3f9m", StepSlug: "seed-search-find-seeds-keyword-cli", Status: garden.StatusGrowing,
				Title: "seed-search: find seeds by keyword from the CLI",
			},
			Where:   garden.MatchBody,
			Snippet: "Agents search the garden before planting, so duplicates get found",
		})
	}
	return result
}

func TestFprintSeedSearchShowsWhereEachHitMatched(t *testing.T) {
	var buf bytes.Buffer
	fprintSeedSearch(&buf, "search garden", searchResult(1, 1, 25))
	out := buf.String()
	for _, want := range []string{
		"1 seed matches \"search garden\"",
		"s-7k3f9m  seed-search-find-seeds-keyword-cli  seed-search: find seeds by keyword from the CLI",
		"growing  body  Agents search the garden before planting",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the answer does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "max_results") {
		t.Fatalf("nothing was trimmed, so nothing should be said about the cap:\n%s", out)
	}
}

func TestFprintSeedSearchNamesTheCapAndTheAskWhenItTrims(t *testing.T) {
	var buf bytes.Buffer
	fprintSeedSearch(&buf, "pty", searchResult(25, 187, 25))
	out := buf.String()
	for _, want := range []string{
		"187 seeds match \"pty\"",
		"showing 25 of 187",
		"max_results=25, asked for 187",
		"--limit <n>` up to 1000",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("a trimmed answer does not say %q:\n%s", want, out)
		}
	}
}

func TestFprintSeedSearchSaysWhatItSearchedWhenNothingMatched(t *testing.T) {
	var buf bytes.Buffer
	fprintSeedSearch(&buf, "xyzzy", searchResult(0, 0, 25))
	out := buf.String()
	if !strings.Contains(out, "no seed matches \"xyzzy\"") || !strings.Contains(out, "477 seeds") {
		t.Fatalf("an empty answer must name the query and what it read:\n%s", out)
	}
}
