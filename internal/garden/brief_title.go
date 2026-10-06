package garden

import "strings"

func TitleFromBrief(brief string) string {
	var title string
	var fence briefCodeFence
	for _, line := range strings.Split(brief, "\n") {
		if title == "" {
			title = strings.TrimSpace(line)
		}
		if fence.length != 0 {
			if fence.closes(line) {
				fence = briefCodeFence{}
			}
			continue
		}
		if fence = openBriefCodeFence(line); fence.length != 0 {
			continue
		}
		if heading := briefATXHeading(line); heading != "" {
			title = heading
			break
		}
	}
	title = strings.Join(strings.Fields(title), " ")
	runes := []rune(title)
	if len(runes) <= 80 {
		return title
	}
	cut := 80
	for i := 80; i > 0; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	return string(runes[:cut])
}

func briefATXHeading(line string) string {
	hashes := len(line) - len(strings.TrimLeft(line, "#"))
	if hashes < 1 || hashes > 6 || hashes == len(line) {
		return ""
	}
	rest := line[hashes:]
	if rest[0] != ' ' && rest[0] != '\t' {
		return ""
	}
	rest = strings.TrimRight(rest, " \t\r")
	withoutHashes := strings.TrimRight(rest, "#")
	if len(withoutHashes) < len(rest) && len(withoutHashes) > 0 {
		last := withoutHashes[len(withoutHashes)-1]
		if last == ' ' || last == '\t' {
			rest = withoutHashes
		}
	}
	return strings.TrimSpace(rest)
}

type briefCodeFence struct {
	marker byte
	length int
}

func openBriefCodeFence(line string) briefCodeFence {
	fence, rest := briefFenceLine(line)
	if fence.marker == '`' && strings.ContainsRune(rest, '`') {
		return briefCodeFence{}
	}
	return fence
}

func (f briefCodeFence) closes(line string) bool {
	candidate, rest := briefFenceLine(line)
	return candidate.marker == f.marker && candidate.length >= f.length && strings.Trim(rest, " \t\r") == ""
}

func briefFenceLine(line string) (briefCodeFence, string) {
	unindented := strings.TrimLeft(line, " ")
	if len(line)-len(unindented) > 3 || len(unindented) < 3 {
		return briefCodeFence{}, ""
	}
	marker := unindented[0]
	if marker != '`' && marker != '~' {
		return briefCodeFence{}, ""
	}
	length := 1
	for length < len(unindented) && unindented[length] == marker {
		length++
	}
	if length < 3 {
		return briefCodeFence{}, ""
	}
	return briefCodeFence{marker: marker, length: length}, unindented[length:]
}
