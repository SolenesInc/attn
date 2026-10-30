package garden

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type OutlinePart struct {
	Heading string
	Bytes   int
	IDs     []string
}

var (
	markdownHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*$`)
	itemID          = regexp.MustCompile(`^(?:[-*+][ \t]+)?\**([A-Z][0-9]+)\**[.:)][ \t*]`)
	listMarker      = regexp.MustCompile(`^(?:[-*+]|[0-9]+[.)])[ \t]`)
	idRef           = regexp.MustCompile(`^[A-Za-z][0-9]+$`)
)

type bodyLine struct {
	text         string
	inFence      bool
	headingLevel int
	heading      string
	id           string
}

func (l bodyLine) blank() bool    { return strings.TrimSpace(l.text) == "" }
func (l bodyLine) indented() bool { return l.text != "" && (l.text[0] == ' ' || l.text[0] == '\t') }
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
			if m := markdownHeading.FindStringSubmatch(unindented); m != nil {
				line.headingLevel, line.heading = len(m[1]), m[2]
			} else if m := itemID.FindStringSubmatch(unindented); m != nil {
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

func Section(body, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	lines := scanBody(body)
	parts := bodyParts(lines)
	if idRef.MatchString(ref) {
		id := strings.ToUpper(ref)
		if items := itemsStartingWith(lines, parts, id); items != "" {
			return items, nil
		}
		return "", fmt.Errorf("no item starts with %s; the parts are: %s", id, DescribeOutline(Outline(body)))
	}
	var prefixed []bodyPart
	for _, part := range parts {
		if strings.EqualFold(part.heading, ref) {
			return joinLines(lines[part.start:part.end]), nil
		}
		if ref != "" && strings.HasPrefix(strings.ToLower(part.heading), strings.ToLower(ref)) {
			prefixed = append(prefixed, part)
		}
	}
	switch len(prefixed) {
	case 1:
		return joinLines(lines[prefixed[0].start:prefixed[0].end]), nil
	case 0:
		return "", fmt.Errorf("no part matches %q; the parts are: %s", ref, DescribeOutline(Outline(body)))
	}
	headings := make([]string, len(prefixed))
	for i, part := range prefixed {
		headings[i] = part.heading
	}
	return "", fmt.Errorf("%q begins %d headings: %s; give more of the one you want", ref, len(prefixed), strings.Join(headings, ", "))
}

func itemsStartingWith(lines []bodyLine, parts []bodyPart, id string) string {
	var blocks []string
	lastHeading := -1
	for i, line := range lines {
		if line.id != id {
			continue
		}
		if p := slices.IndexFunc(parts, func(part bodyPart) bool { return part.start < i && i < part.end }); p >= 0 && p != lastHeading {
			blocks = append(blocks, lines[parts[p].start].text)
			lastHeading = p
		}
		blocks = append(blocks, joinLines(lines[i:itemEnd(lines, i)]))
	}
	return strings.Join(blocks, "\n\n")
}

func itemEnd(lines []bodyLine, start int) int {
	listItem := listMarker.MatchString(lines[start].text)
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if line.inFence {
			continue
		}
		if line.headingLevel > 0 || line.id != "" {
			return i
		}
		if line.blank() || line.indented() {
			continue
		}
		startsList := listMarker.MatchString(line.text)
		if listItem && startsList {
			return i
		}
		if lines[i-1].blank() && !(startsList && !listItem) {
			return i
		}
	}
	return len(lines)
}

func DescribeOutline(outline []OutlinePart) string {
	if len(outline) == 0 {
		return "none, the body has no headings"
	}
	described := make([]string, len(outline))
	for i, part := range outline {
		described[i] = part.Heading
		if ids := IDRanges(part.IDs); ids != "" {
			described[i] += " " + ids
		}
	}
	return strings.Join(described, " · ")
}

func IDRanges(ids []string) string {
	var letters []byte
	numbers := map[byte][]int{}
	for _, id := range ids {
		n, err := strconv.Atoi(id[1:])
		if err != nil {
			continue
		}
		if _, seen := numbers[id[0]]; !seen {
			letters = append(letters, id[0])
		}
		numbers[id[0]] = append(numbers[id[0]], n)
	}
	var runs []string
	for _, letter := range letters {
		ns := numbers[letter]
		slices.Sort(ns)
		ns = slices.Compact(ns)
		for i := 0; i < len(ns); {
			j := i
			for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
				j++
			}
			switch {
			case j-i >= 2:
				runs = append(runs, fmt.Sprintf("%c%d–%c%d", letter, ns[i], letter, ns[j]))
			case j == i+1:
				runs = append(runs, fmt.Sprintf("%c%d", letter, ns[i]), fmt.Sprintf("%c%d", letter, ns[j]))
			default:
				runs = append(runs, fmt.Sprintf("%c%d", letter, ns[i]))
			}
			i = j + 1
		}
	}
	return strings.Join(runs, ", ")
}
