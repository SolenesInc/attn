package garden

import (
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
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
		return nil, protocol.Refuse(protocol.RefusalLimit, "max_query_chars=%d, asked for %d; search takes the words that name the work, not a whole body", MaxSearchQueryChars, n)
	}
	words := docstore.Words(trimmed)
	if len(words) == 0 {
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "a search needs words to look for: `{attn seed search <words>}`")
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

func JoinSeedText(titleRows, bodyRows []docstore.TextMatch, words int) []docstore.TextMatch {
	byID := make(map[string]int, len(titleRows)+len(bodyRows))
	out := make([]docstore.TextMatch, 0, len(titleRows)+len(bodyRows))
	row := func(match docstore.TextMatch) *docstore.TextMatch {
		at, seen := byID[match.ID]
		if !seen {
			at = len(out)
			byID[match.ID] = at
			out = append(out, docstore.TextMatch{
				ID: match.ID, Key: match.Key, CreatedAt: match.CreatedAt,
				Score:   make([]float64, 2),
				Carries: [][]bool{make([]bool, words), make([]bool, words)},
			})
		}
		return &out[at]
	}
	for field, rows := range [][]docstore.TextMatch{titleRows, bodyRows} {
		for _, match := range rows {
			joined := row(match)
			joined.Score[field], joined.Carries[field] = match.Score[0], match.Carries[0]
		}
	}
	return out
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
				r.hit.Quoted = SearchQuote{[]docstore.CollectionSchema{SeedsSchema(), SeedBodiesSchema()}[field], row.ID, 0}
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

func Similar(titleRows [][]docstore.TextMatch, seeds []Seed, excludedIDs []string) []SimilarHit {
	excluded := map[string]bool{}
	for _, id := range excludedIDs {
		excluded[id] = true
	}
	open := map[string]Seed{}
	for _, seed := range seeds {
		if !Closed(seed.Status) && !excluded[seed.ID] {
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
	snippet, _ := SnippetWithSpans(text, spans)
	return snippet
}

func SnippetWithSpans(text string, spans [][2]int) (string, [][2]int) {
	line, lineStart, at := bestLine(text, spans)
	line, leftTrim, at := trimLine(line, at)
	lineStart += leftTrim
	runes := []rune(line)
	if len(runes) <= SnippetChars {
		return line, projectSpans(spans, lineStart, lineStart+len(line), 0)
	}
	start := max(0, len([]rune(line[:at]))-SnippetChars/3)
	if start+SnippetChars > len(runes) {
		start = len(runes) - SnippetChars
	}
	sliceStart := runeByteOffset(line, start)
	sliceEnd := runeByteOffset(line, start+SnippetChars)
	out := line[sliceStart:sliceEnd]
	prefixBytes := 0
	if start > 0 {
		out = "…" + out
		prefixBytes = len("…")
	}
	if start+SnippetChars < len(runes) {
		out += "…"
	}
	return out, projectSpans(spans, lineStart+sliceStart, lineStart+sliceEnd, prefixBytes)
}

func runeByteOffset(s string, target int) int {
	if target <= 0 {
		return 0
	}
	count := 0
	for i := range s {
		if count == target {
			return i
		}
		count++
	}
	return len(s)
}

func projectSpans(spans [][2]int, winStart, winEnd, shift int) [][2]int {
	var projected [][2]int
	for _, span := range spans {
		s := max(span[0], winStart)
		e := min(span[1], winEnd)
		if e <= s {
			continue
		}
		projected = append(projected, [2]int{s - winStart + shift, e - winStart + shift})
	}
	return MergeSpans(projected)
}

func MergeSpans(spans [][2]int) [][2]int {
	if len(spans) <= 1 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i][0] != spans[j][0] {
			return spans[i][0] < spans[j][0]
		}
		return spans[i][1] < spans[j][1]
	})
	merged := spans[:0]
	for _, span := range spans {
		if len(merged) > 0 && span[0] <= merged[len(merged)-1][1] {
			if span[1] > merged[len(merged)-1][1] {
				merged[len(merged)-1][1] = span[1]
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func TextRanges(haystack string, terms []string) [][2]int {
	lower := strings.ToLower(haystack)
	if len(lower) != len(haystack) {
		return nil
	}
	var found [][2]int
	for _, term := range terms {
		term = strings.ToLower(term)
		if term == "" {
			continue
		}
		from := 0
		for from < len(lower) {
			at := strings.Index(lower[from:], term)
			if at < 0 {
				break
			}
			start := from + at
			end := start + len(term)
			found = append(found, [2]int{start, end})
			from = end
		}
	}
	return MergeSpans(found)
}

func bestLine(text string, spans [][2]int) (string, int, int) {
	best, _, _ := strings.Cut(text, "\n")
	bestStart, bestCount, bestAt, offset := 0, 0, 0, 0
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
			bestCount, best, bestStart, bestAt = count, strings.TrimRight(line, "\n"), offset, first
		}
		offset += len(line)
	}
	return best, bestStart, bestAt
}

func trimLine(line string, at int) (string, int, int) {
	trimmed := strings.TrimLeft(line, " \t")
	leftTrim := len(line) - len(trimmed)
	at -= leftTrim
	trimmed = strings.TrimRight(trimmed, " \t")
	return trimmed, leftTrim, min(max(at, 0), len(trimmed))
}
