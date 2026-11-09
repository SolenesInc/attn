package garden

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

type OutlinePart struct {
	Heading string
	Bytes   int
	IDs     []string
}

var (
	markdownHeading = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*$`)
	})
	itemID = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`^(?:[-*+][ \t]+)?\**([A-Z][0-9]+)\**[.:)][ \t*]`)
	})
)

type bodyLine struct {
	text         string
	inFence      bool
	headingLevel int
	heading      string
	id           string
}

func (l bodyLine) partStart() bool {
	return l.headingLevel == 1 || l.headingLevel == 2
}

func scanBody(body string) []bodyLine {
	raw := strings.Split(body, "\n")
	lines := make([]bodyLine, len(raw))
	fence := ""
	for i, text := range raw {
		line := bodyLine{text: text}
		trimmed := strings.TrimLeft(strings.TrimRight(text, "\r"), " \t")
		switch {
		case fence != "":
			line.inFence = true
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```"):
			fence = "```"
		case strings.HasPrefix(trimmed, "~~~"):
			fence = "~~~"
		default:
			unindented := strings.TrimRight(text, "\r")
			if m := markdownHeading().FindStringSubmatch(unindented); m != nil {
				line.headingLevel, line.heading = len(m[1]), m[2]
			} else if m := itemID().FindStringSubmatch(unindented); m != nil {
				line.id = m[1]
			}
		}
		lines[i] = line
	}
	return lines
}

type bodyPart struct {
	heading    string
	start, end int
}

func bodyParts(lines []bodyLine) []bodyPart {
	var parts []bodyPart
	for i, line := range lines {
		if !line.partStart() {
			continue
		}
		if n := len(parts); n > 0 {
			parts[n-1].end = i
		}
		parts = append(parts, bodyPart{heading: line.heading, start: i, end: len(lines)})
	}
	return parts
}

func joinLines(lines []bodyLine) string {
	texts := make([]string, len(lines))
	for i, line := range lines {
		texts[i] = line.text
	}
	return strings.TrimRight(strings.Join(texts, "\n"), "\r\n\t ")
}

func Outline(body string) []OutlinePart {
	lines := scanBody(body)
	var outline []OutlinePart
	for _, part := range bodyParts(lines) {
		ids := []string{}
		for _, line := range lines[part.start+1 : part.end] {
			if line.id != "" && !slices.Contains(ids, line.id) {
				ids = append(ids, line.id)
			}
		}
		outline = append(outline, OutlinePart{
			Heading: part.heading,
			Bytes:   len(joinLines(lines[part.start:part.end])),
			IDs:     ids,
		})
	}
	return outline
}
