package garden

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MatchTitle = "title"
	MatchBody  = "body"
	MatchLog   = "log"
)

// Measured against a 477-seed, 1,134-note garden: `garden search` matches 17 seeds,
// `harvest` 95, `pty` 187; body and note lines run median 72 characters, p75 92.
const (
	DefaultSearchResults = 25
	MaxSearchResults     = 1000
	MaxSearchQueryChars  = MaxTitleChars
	SnippetChars         = 160
)

type SearchSubject struct {
	Seed Seed
	Log  []string
}

type SearchHit struct {
	Seed    Seed
	Where   string
	Snippet string
}

func SearchTerms(query string) ([]string, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, fmt.Errorf("a search needs something to look for: `attn seed search <words>`")
	}
	if n := len([]rune(trimmed)); n > MaxSearchQueryChars {
		return nil, fmt.Errorf("max_query_chars=%d, asked for %d; search takes the words that name the work, not a whole body", MaxSearchQueryChars, n)
	}
	return strings.Fields(strings.ToLower(trimmed)), nil
}

func Search(subjects []SearchSubject, terms []string, limit int) (hits []SearchHit, matched int) {
	if len(terms) == 0 {
		return nil, 0
	}
	if limit <= 0 {
		limit = DefaultSearchResults
	}
	byWhere := map[string][]SearchHit{}
	for _, subject := range subjects {
		hit, ok := searchSubject(subject, terms)
		if !ok {
			continue
		}
		matched++
		byWhere[hit.Where] = append(byWhere[hit.Where], hit)
	}
	hits = make([]SearchHit, 0, min(matched, limit))
	for _, where := range []string{MatchTitle, MatchBody, MatchLog} {
		for _, hit := range byWhere[where] {
			if len(hits) == limit {
				return hits, matched
			}
			hits = append(hits, hit)
		}
	}
	return hits, matched
}

func searchSubject(subject SearchSubject, terms []string) (SearchHit, bool) {
	fields := []struct {
		where string
		text  string
	}{
		{MatchTitle, subject.Seed.Title},
		{MatchBody, subject.Seed.Body},
	}
	for _, entry := range subject.Log {
		fields = append(fields, struct {
			where string
			text  string
		}{MatchLog, entry})
	}
	for _, field := range fields {
		if !carriesAll(field.text, terms) {
			continue
		}
		return SearchHit{Seed: subject.Seed, Where: field.where, Snippet: Snippet(field.text, terms)}, true
	}
	return SearchHit{}, false
}

func carriesAll(text string, terms []string) bool {
	lowered := strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(lowered, term) {
			return false
		}
	}
	return true
}

func Snippet(text string, terms []string) string {
	line, at := bestLine(text, terms)
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

func bestLine(text string, terms []string) (string, int) {
	bestCarried, bestLine, bestAt := 0, firstLineOf(text), 0
	for line := range strings.Lines(text) {
		carried, at := 0, -1
		for _, term := range terms {
			found := foldedIndex(line, term)
			if found < 0 {
				continue
			}
			carried++
			if at < 0 || found < at {
				at = found
			}
		}
		if carried > bestCarried {
			bestCarried, bestLine, bestAt = carried, line, at
		}
		if carried == len(terms) {
			break
		}
	}
	return strings.TrimRight(bestLine, "\n"), bestAt
}

func firstLineOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// foldedIndex is strings.Index over lowered text, answered in the original
// text's byte offsets: lowering a rune can change its encoded length.
func foldedIndex(text, loweredTerm string) int {
	at := strings.Index(strings.ToLower(text), loweredTerm)
	if at <= 0 {
		return at
	}
	lowered := 0
	for i, r := range text {
		if lowered >= at {
			return i
		}
		lowered += utf8.RuneLen(unicode.ToLower(r))
	}
	return len(text)
}

func trimLine(line string, at int) (string, int) {
	trimmed := strings.TrimLeft(line, " \t")
	at -= len(line) - len(trimmed)
	trimmed = strings.TrimRight(trimmed, " \t")
	return trimmed, min(max(at, 0), len(trimmed))
}
