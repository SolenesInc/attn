package garden

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

const (
	MatchTitle = "title"
	MatchBody  = "body"
	MatchLog   = "log"
)

const (
	DefaultSearchResults = 10
	MaxSearchResults     = 1000
	MaxSearchQueryChars  = MaxTitleChars
	SnippetChars         = 160
)

type SearchQuote struct {
	Schema docstore.CollectionSchema
	ID     string
	Field  int
}

type SearchHit struct {
	Seed       Seed
	Where      string
	LastActive time.Time
	Quoted     SearchQuote
}

type Ranked struct {
	Hits    []SearchHit
	Matched int
	Partial int
}

func SearchWords(query string) ([]string, error) {
	trimmed := strings.TrimSpace(query)
	if n := len([]rune(trimmed)); n > MaxSearchQueryChars {
		return nil, fmt.Errorf("max_query_chars=%d, asked for %d; search takes the words that name the work, not a whole body", MaxSearchQueryChars, n)
	}
	words := docstore.Words(trimmed)
	if len(words) == 0 {
		return nil, fmt.Errorf("a search needs words to look for: `attn seed search <words>`")
	}
	return words, nil
}

type rankedSeed struct {
	hit            SearchHit
	carries        []bool
	titleAll       bool
	coverage       int
	quotedCoverage int
	quotedAt       time.Time
	bm25           float64
	logScore       float64
	rrf            float64
	position       int
}

func Rank(words []string, seedRows, noteRows []docstore.TextMatch, seeds []Seed) Ranked {
	byID := map[string]*rankedSeed{}
	for _, seed := range seeds {
		stateAt, _ := time.Parse(time.RFC3339Nano, seed.StateChangedAt)
		byID[seed.ID] = &rankedSeed{hit: SearchHit{Seed: seed, LastActive: stateAt}, carries: make([]bool, len(words))}
	}
	for _, row := range seedRows {
		r := byID[row.ID]
		if r == nil {
			continue
		}
		if row.CreatedAt.After(r.hit.LastActive) {
			r.hit.LastActive = row.CreatedAt
		}
		for field, carried := range row.Carries {
			count := mergeCarried(r.carries, carried)
			if field == 0 {
				r.titleAll = count == len(words)
			}
			if count > r.quotedCoverage {
				r.quotedCoverage = count
				r.hit.Quoted = SearchQuote{SeedsSchema(), row.ID, field}
				r.hit.Where = []string{MatchTitle, MatchBody}[field]
			}
		}
		r.bm25 = 10*row.Score[0] + 3*row.Score[1]
	}
	for _, row := range noteRows {
		r := byID[row.Key]
		if r == nil {
			continue
		}
		if row.CreatedAt.After(r.hit.LastActive) {
			r.hit.LastActive = row.CreatedAt
		}
		r.logScore = max(r.logScore, row.Score[0])
		count := mergeCarried(r.carries, row.Carries[0])
		if count > r.quotedCoverage || count == r.quotedCoverage && r.hit.Where == MatchLog && row.CreatedAt.After(r.quotedAt) {
			r.quotedCoverage, r.quotedAt = count, row.CreatedAt
			r.hit.Quoted = SearchQuote{NotesSchema(), row.ID, 0}
			r.hit.Where = MatchLog
		}
	}
	var rows []*rankedSeed
	out := Ranked{Hits: []SearchHit{}}
	for _, seed := range seeds {
		r := byID[seed.ID]
		for _, carried := range r.carries {
			if carried {
				r.coverage++
			}
		}
		if r.coverage == 0 {
			continue
		}
		if r.coverage == len(words) {
			out.Matched++
		} else {
			out.Partial++
		}
		r.bm25 += r.logScore
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].bm25 > rows[j].bm25 })
	for i, row := range rows {
		row.rrf = 1 / float64(10+i)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].hit.LastActive.After(rows[j].hit.LastActive) })
	for i, row := range rows {
		row.rrf += 1 / float64(10+i)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.titleAll != b.titleAll {
			return a.titleAll
		}
		if a.coverage != b.coverage {
			return a.coverage > b.coverage
		}
		return a.rrf > b.rrf
	})
	for i, row := range rows {
		row.position = i
		if !Closed(row.hit.Seed.Status) {
			row.position -= 5
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].position < rows[j].position })
	for _, row := range rows {
		out.Hits = append(out.Hits, row.hit)
	}
	return out
}

type SimilarHit struct {
	Seed       Seed
	LastActive time.Time
	TitleIndex int
}

func Similar(titleRows [][]docstore.TextMatch, seeds []Seed, parent string) []SimilarHit {
	open := map[string]Seed{}
	for _, seed := range seeds {
		if !Closed(seed.Status) && seed.ID != parent {
			open[seed.ID] = seed
		}
	}
	lists := make([][]docstore.TextMatch, len(titleRows))
	for i, rows := range titleRows {
		for _, row := range rows {
			if _, ok := open[row.ID]; ok {
				lists[i] = append(lists[i], row)
			}
		}
		sort.SliceStable(lists[i], func(a, b int) bool {
			x, y := lists[i][a], lists[i][b]
			return 10*x.Score[0]+x.Score[1] > 10*y.Score[0]+y.Score[1]
		})
	}
	out := []SimilarHit{}
	seen := map[string]bool{}
	for rank := 0; ; rank++ {
		any := false
		for title, rows := range lists {
			if rank >= len(rows) {
				continue
			}
			any = true
			row := rows[rank]
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			seed := open[row.ID]
			stateAt, _ := time.Parse(time.RFC3339Nano, seed.StateChangedAt)
			if row.CreatedAt.After(stateAt) {
				stateAt = row.CreatedAt
			}
			out = append(out, SimilarHit{Seed: seed, LastActive: stateAt, TitleIndex: title})
			if len(out) == 3 {
				return out
			}
		}
		if !any {
			return out
		}
	}
}

func mergeCarried(into, from []bool) int {
	count := 0
	for i, carried := range from {
		if carried {
			into[i] = true
			count++
		}
	}
	return count
}

func Snippet(text string, spans [][2]int) string {
	line, at := bestLine(text, spans)
	line, at = trimLine(line, at)
	runes := []rune(line)
	if len(runes) <= SnippetChars {
		return line
	}
	start := max(0, len([]rune(line[:at]))-SnippetChars/3)
	if start+SnippetChars > len(runes) {
		start = len(runes) - SnippetChars
	}
	out := string(runes[start : start+SnippetChars])
	if start > 0 {
		out = "…" + out
	}
	if start+SnippetChars < len(runes) {
		out += "…"
	}
	return out
}

func bestLine(text string, spans [][2]int) (string, int) {
	best, _, _ := strings.Cut(text, "\n")
	bestCount, bestAt, offset := 0, 0, 0
	for line := range strings.Lines(text) {
		count, first := 0, -1
		for _, span := range spans {
			if span[0] >= offset && span[0] < offset+len(line) {
				count++
				if first < 0 || span[0]-offset < first {
					first = span[0] - offset
				}
			}
		}
		if count > bestCount {
			bestCount, best, bestAt = count, strings.TrimRight(line, "\n"), first
		}
		offset += len(line)
	}
	return best, bestAt
}

func trimLine(line string, at int) (string, int) {
	trimmed := strings.TrimLeft(line, " \t")
	at -= len(line) - len(trimmed)
	trimmed = strings.TrimRight(trimmed, " \t")
	return trimmed, min(max(at, 0), len(trimmed))
}
