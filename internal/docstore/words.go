package docstore

import (
	"sort"
	"strings"
	"unicode"
)

type identifier struct {
	start, end int
	parts      []string
}

func identifiers(text string) []identifier {
	var out []identifier
	start := -1
	for at, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if start < 0 {
				start = at
			}
		} else if start >= 0 {
			out = append(out, splitIdentifier(text, start, at))
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, splitIdentifier(text, start, len(text)))
	}
	return out
}

func splitIdentifier(text string, start, end int) identifier {
	id := identifier{start: start, end: end}
	for _, word := range strings.Split(text[start:end], "_") {
		runes := []rune(word)
		from := 0
		for i := 1; i < len(runes); i++ {
			if unicode.IsUpper(runes[i]) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
				id.parts = append(id.parts, string(runes[from:i]))
				from = i
			}
		}
		if from < len(runes) {
			id.parts = append(id.parts, string(runes[from:]))
		}
	}
	return id
}

func Words(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(word string) {
		word = strings.ToLower(word)
		if word != "" && !seen[word] {
			out = append(out, word)
			seen[word] = true
		}
	}
	for _, id := range identifiers(text) {
		for _, word := range strings.Split(text[id.start:id.end], "_") {
			add(word)
		}
		for _, part := range id.parts {
			add(part)
		}
	}
	return out
}

type textAlias struct {
	start, end int
	original   [2]int
}

func indexedText(text string) (string, []textAlias) {
	var out strings.Builder
	for _, r := range text {
		if r == '_' || r >= 0x80 && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			for range len(string(r)) {
				out.WriteByte(' ')
			}
		} else {
			out.WriteRune(r)
		}
	}
	var aliases []textAlias
	for _, id := range identifiers(text) {
		var words []string
		wholeWords := strings.FieldsFunc(text[id.start:id.end], func(r rune) bool { return r == '_' })
		for _, word := range wholeWords {
			lower := strings.ToLower(word)
			if lower != word && strings.IndexFunc(word, func(r rune) bool { return r >= 0x80 }) >= 0 {
				words = append(words, lower)
			}
		}
		if len(id.parts) > len(wholeWords) {
			words = append(words, id.parts...)
		}
		if len(words) > 0 {
			out.WriteByte('\n')
			start := out.Len()
			out.WriteString(strings.ToLower(strings.Join(words, " ")))
			aliases = append(aliases, textAlias{start, out.Len(), [2]int{id.start, id.end}})
		}
	}
	return out.String(), aliases
}

func IndexText(text string) string {
	indexed, _ := indexedText(text)
	return indexed
}

func OriginalSpans(text string, spans [][2]int) [][2]int {
	_, aliases := indexedText(text)
	out := make([][2]int, 0, len(spans))
	seen := map[[2]int]bool{}
	for _, span := range spans {
		if span[0] >= len(text) {
			i := sort.Search(len(aliases), func(i int) bool { return aliases[i].end >= span[1] })
			if i < len(aliases) && span[0] >= aliases[i].start {
				span = aliases[i].original
			}
		}
		if span[0] < len(text) && !seen[span] {
			out = append(out, span)
			seen[span] = true
		}
	}
	return out
}
