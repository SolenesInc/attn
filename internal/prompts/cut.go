package prompts

import "unicode/utf8"

func CutAtRune(text string, limit int) string {
	if limit >= len(text) {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
