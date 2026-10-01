package codexshared

import "strings"

// ResolveTitle returns a root only when the native title's displayed ID token
// matches exactly one known root. Decorations and truncation carry no identity.
func ResolveTitle(title string, roots []string) string {
	var token string
	for _, word := range strings.Fields(title) {
		candidate := strings.TrimSuffix(strings.TrimSuffix(word, "..."), "…")
		if candidate == "" {
			continue
		}
		valid := true
		for _, ch := range candidate {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch == '-') {
				valid = false
				break
			}
		}
		if valid && strings.Contains(candidate, "-") {
			token = candidate
			break
		}
	}
	if token == "" {
		return ""
	}
	result := ""
	for _, root := range roots {
		if strings.HasPrefix(root, token) {
			if result != "" {
				return ""
			}
			result = root
		}
	}
	return result
}
