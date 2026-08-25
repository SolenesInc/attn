package activity

import (
	"strings"
	"unicode/utf8"
)

// Never invents content: an empty or preamble-only answer comes back not-ok so the caller keeps the previous line.
func Sanitize(raw string) (string, bool) {
	line := firstMeaningfulLine(raw)
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`")
	line = strings.TrimSpace(line)

	if len(line) >= 2 {
		first, last := line[0], line[len(line)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			line = strings.TrimSpace(line[1 : len(line)-1])
		}
	}

	line = strings.TrimRight(line, " .…")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}

	if utf8.RuneCountInString(line) > MaxLineRunes {
		runes := []rune(line)
		line = strings.TrimRight(string(runes[:MaxLineRunes-1]), " ,;:-") + "…"
	}
	return line, true
}

func firstMeaningfulLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		return trimmed
	}
	return ""
}
