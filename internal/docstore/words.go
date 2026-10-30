package docstore

import (
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

func IndexText(text string) string {
	var out strings.Builder
	out.WriteString(text)
	for _, id := range identifiers(text) {
		if len(id.parts) > 1 {
			out.WriteByte('\n')
			out.WriteString(strings.Join(id.parts, " "))
		}
	}
	return out.String()
}

func OriginalSpans(text string, spans [][2]int) [][2]int {
	type appended struct {
		start, end int
		original   [2]int
	}
	var lines []appended
	at := len(text)
	for _, id := range identifiers(text) {
		if len(id.parts) > 1 {
			end := at + 1 + len(strings.Join(id.parts, " "))
			lines = append(lines, appended{at + 1, end, [2]int{id.start, id.end}})
			at = end
		}
	}
	out := make([][2]int, 0, len(spans))
	seen := map[[2]int]bool{}
	for _, span := range spans {
		if span[0] >= len(text) {
			for _, line := range lines {
				if span[0] >= line.start && span[1] <= line.end {
					span = line.original
					break
				}
			}
		}
		if span[0] < len(text) && !seen[span] {
			out = append(out, span)
			seen[span] = true
		}
	}
	return out
}
