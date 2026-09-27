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

func TestSearch(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		limit   int
		matched int
		hits    []string
		where   []string
		snippet string
	}{
		{name: "closed seeds answer whether it was already done", query: "tickets", matched: 1, hits: []string{"s-cccccc"}},
		{name: "a log-only match says so and quotes the note", query: "prototyped", matched: 1, hits: []string{"s-bbbbbb"}, where: []string{MatchLog}, snippet: "Prototyped the drop target"},
		{name: "terms scattered across fields do not match", query: "index milliseconds", matched: 0},
		{name: "terms in one note match", query: "scan search", matched: 1, hits: []string{"s-dddddd"}},
		{name: "title matches come before body matches", query: "seed", matched: 3, where: []string{MatchTitle, MatchTitle, MatchBody}},
		{name: "a limit trims the answer and keeps the count", query: "seed", limit: 1, matched: 3, where: []string{MatchTitle}},
		{name: "case does not decide a match", query: "SQLite FTS", matched: 1, hits: []string{"s-dddddd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terms, err := SearchTerms(tc.query)
			if err != nil {
				t.Fatalf("terms for %q: %v", tc.query, err)
			}
			hits, matched := Search(subjects(), terms, tc.limit)
			if matched != tc.matched {
				t.Fatalf("matched %d, want %d: %+v", matched, tc.matched, hits)
			}
			if tc.hits != nil {
				var ids []string
				for _, hit := range hits {
					ids = append(ids, hit.Seed.ID)
				}
				if !equal(ids, tc.hits) {
					t.Errorf("hits = %v, want %v", ids, tc.hits)
				}
			}
			if tc.where != nil {
				var where []string
				for _, hit := range hits {
					where = append(where, hit.Where)
				}
				if !equal(where, tc.where) {
					t.Errorf("matched in %v, want %v", where, tc.where)
				}
			}
			if tc.snippet != "" && !strings.Contains(hits[0].Snippet, tc.snippet) {
				t.Errorf("snippet %q does not quote %q", hits[0].Snippet, tc.snippet)
			}
		})
	}
}

func TestSnippet(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		terms  []string
		check  func(snippet string) bool
		expect string
	}{
		{
			name:  "a long line is elided on both sides around the match within the budget",
			text:  "first line\n" + strings.Repeat("padding ", 40) + "NEEDLE" + strings.Repeat(" trailing", 40),
			terms: []string{"needle"},
			check: func(s string) bool {
				return strings.Contains(s, "NEEDLE") && len([]rune(s)) <= SnippetChars+2 && strings.HasPrefix(s, "…") && strings.HasSuffix(s, "…")
			},
			expect: "the match, cut on both sides, within the budget",
		},
		{
			name:   "a short line prints whole and unindented",
			text:   "  a note about the garden  ",
			terms:  []string{"garden"},
			check:  func(s string) bool { return s == "a note about the garden" },
			expect: "the whole line",
		},
		{
			name:   "case folding that changes byte length keeps the match intact",
			text:   strings.Repeat("İ", 30) + " the needle is here " + strings.Repeat("ü", 200),
			terms:  []string{"needle"},
			check:  func(s string) bool { return strings.Contains(s, "needle is here") },
			expect: "the match intact",
		},
		{
			name:   "the line carrying every term wins",
			text:   "# Finish the garden\n\n" + strings.Repeat("filler about plots and edges\n", 20) + "Search is its own verb, with snippets across the whole garden.\n",
			terms:  []string{"garden", "search"},
			check:  func(s string) bool { return strings.HasPrefix(s, "Search is its own verb") },
			expect: "the line saying both terms",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Snippet(tc.text, tc.terms); !tc.check(got) {
				t.Errorf("Snippet = %q, want %s", got, tc.expect)
			}
		})
	}
}
