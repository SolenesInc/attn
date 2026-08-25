package notebook

import "regexp"

// Root-absolute markdown targets only, by convention — no [[wikilinks]], and
// relative/external/anchor-only targets are deliberately unmatched.
var rootAbsoluteLinkRE = regexp.MustCompile(`\[[^\]]*\]\((/[^)\s]+)\)`)

func Links(body string) []string {
	matches := rootAbsoluteLinkRE.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		target := m[1]
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}
