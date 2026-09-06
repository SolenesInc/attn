package garden

import (
	"strings"
	"testing"
)

func subjects() []SearchSubject {
	return []SearchSubject{
		{Seed: Seed{
			ID: "s-aaaaaa", Title: "seed-search: find seeds by keyword from the CLI", Status: StatusGrowing,
			Body: "Agents are expected to search the garden before planting.\nDone looks like a CLI query.",
		}},
		{Seed: Seed{
			ID: "s-bbbbbb", Title: "Board: dropping a seed on Growing offers dispatch", Status: StatusPlanted,
			Body: "The board can already move a seed between columns.",
		}, Log: []string{"Prototyped the drop target and it feels right.", "Parked until the panel lands."}},
		{Seed: Seed{
			ID: "s-cccccc", Title: "Retire tickets", Status: StatusHarvested,
			Body: "Tickets are gone; every seed lives in the garden now.",
		}, Log: []string{"Migrated 41 tickets into seeds and removed the ticket surface."}},
		{Seed: Seed{
			ID: "s-dddddd", Title: "Full-text index for the docstore", Status: StatusWithered,
			Body: "SQLite FTS over every collection.",
		}, Log: []string{"Withered: a scan answers this search in a couple of milliseconds, so nothing more is needed."}},
	}
}

func searchFor(t *testing.T, query string, limit int) ([]SearchHit, int) {
	t.Helper()
	terms, err := SearchTerms(query)
	if err != nil {
		t.Fatalf("terms for %q: %v", query, err)
	}
	return Search(subjects(), terms, limit)
}

func TestSearchReachesClosedSeeds(t *testing.T) {
	hits, matched := searchFor(t, "tickets", 0)
	if matched != 1 || len(hits) != 1 {
		t.Fatalf("wanted the harvested seed alone, got %d hits of %d matched: %+v", len(hits), matched, hits)
	}
	if hits[0].Seed.ID != "s-cccccc" || hits[0].Seed.Status != StatusHarvested {
		t.Fatalf("a harvested seed is the answer to `was this already done`, got %+v", hits[0])
	}
}

func TestSearchMatchesLogTextAloneAndQuotesIt(t *testing.T) {
	hits, matched := searchFor(t, "prototyped", 0)
	if matched != 1 {
		t.Fatalf("wanted the one seed whose log says it, matched %d", matched)
	}
	if hits[0].Seed.ID != "s-bbbbbb" || hits[0].Where != MatchLog {
		t.Fatalf("a log-only match must say it came from the log, got %+v", hits[0])
	}
	if !strings.Contains(hits[0].Snippet, "Prototyped the drop target") {
		t.Fatalf("the snippet must quote the matching note, got %q", hits[0].Snippet)
	}
}

func TestSearchNeedsEveryTermInOneField(t *testing.T) {
	if _, matched := searchFor(t, "index milliseconds", 0); matched != 0 {
		t.Fatalf("terms scattered across fields matched %d seeds; a snippet could not show why", matched)
	}
	if _, matched := searchFor(t, "scan search", 0); matched != 1 {
		t.Fatalf("both terms sit in one note, so that seed matches; matched %d", matched)
	}
}

func TestSearchOrdersTitleMatchesFirst(t *testing.T) {
	hits, matched := searchFor(t, "seed", 0)
	if matched != 3 {
		t.Fatalf("wanted the three seeds saying `seed`, matched %d", matched)
	}
	if hits[0].Where != MatchTitle || hits[1].Where != MatchTitle {
		t.Fatalf("title matches come first, got %s then %s", hits[0].Where, hits[1].Where)
	}
	if hits[2].Where != MatchBody {
		t.Fatalf("a body match comes after every title match, got %s", hits[2].Where)
	}
}

func TestSearchLimitTrimsAndStillCountsWhatMatched(t *testing.T) {
	hits, matched := searchFor(t, "seed", 1)
	if len(hits) != 1 || matched != 3 {
		t.Fatalf("a limit trims the answer and keeps the count, got %d hits of %d", len(hits), matched)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	if _, matched := searchFor(t, "SQLite FTS", 0); matched != 1 {
		t.Fatalf("case must not decide a match, matched %d", matched)
	}
}

func TestSearchTermsRefusesNothingToLookFor(t *testing.T) {
	if _, err := SearchTerms("   "); err == nil {
		t.Fatal("an empty query must say what to type instead")
	}
	long := strings.Repeat("x", MaxSearchQueryChars+1)
	err := func() error { _, err := SearchTerms(long); return err }()
	if err == nil {
		t.Fatal("an oversized query must be refused")
	}
	if !strings.Contains(err.Error(), "max_query_chars=400") || !strings.Contains(err.Error(), "asked for 401") {
		t.Fatalf("the refusal must name the limit and the ask, got %q", err)
	}
}

func TestSnippetElidesAroundTheMatchAndKeepsItVisible(t *testing.T) {
	line := strings.Repeat("padding ", 40) + "NEEDLE" + strings.Repeat(" trailing", 40)
	snippet := Snippet("first line\n"+line, []string{"needle"})
	if !strings.Contains(snippet, "NEEDLE") {
		t.Fatalf("an elided snippet that drops the match explains nothing: %q", snippet)
	}
	if n := len([]rune(snippet)); n > SnippetChars+2 {
		t.Fatalf("snippet_chars=%d, got %d runes: %q", SnippetChars, n, snippet)
	}
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("an elided snippet must show it was cut on both sides: %q", snippet)
	}
}

func TestSnippetQuotesShortLinesWhole(t *testing.T) {
	if got := Snippet("  a note about the garden  ", []string{"garden"}); got != "a note about the garden" {
		t.Fatalf("a line inside the budget prints whole and unindented, got %q", got)
	}
}

func TestSnippetSurvivesCaseFoldingThatChangesByteLength(t *testing.T) {
	text := strings.Repeat("İ", 30) + " the needle is here " + strings.Repeat("ü", 200)
	snippet := Snippet(text, []string{"needle"})
	if !strings.Contains(snippet, "needle is here") {
		t.Fatalf("the match was lost or garbled: %q", snippet)
	}
}

func TestSnippetPrefersTheLineCarryingEveryTerm(t *testing.T) {
	body := "# Finish the garden\n\n" + strings.Repeat("filler about plots and edges\n", 20) +
		"Search is its own verb, with snippets across the whole garden.\n"
	got := Snippet(body, []string{"garden", "search"})
	if !strings.HasPrefix(got, "Search is its own verb") {
		t.Fatalf("the snippet must quote the line that says both terms, got %q", got)
	}
}
