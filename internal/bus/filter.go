package bus

import "strings"

type Filter []string

var All = Filter{"*"}

// An empty expression means All, so a consumer row written without a filter
// still receives events.
func ParseFilter(expr string) Filter {
	var out Filter
	for _, part := range strings.Split(expr, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return All
	}
	return out
}

func (f Filter) String() string {
	if len(f) == 0 {
		return "*"
	}
	return strings.Join(f, ",")
}

func (f Filter) Matches(name string) bool {
	if len(f) == 0 {
		return true
	}
	for _, pattern := range f {
		if matchPattern(pattern, name) {
			return true
		}
	}
	return false
}

// Exported so the app runtime resolves which declared subscription a fact came
// from with the very rule that delivered it, rather than a copy free to drift.
func MatchPattern(pattern, name string) bool { return matchPattern(pattern, name) }

func matchPattern(pattern, name string) bool {
	switch {
	case pattern == "*" || pattern == "":
		return true
	case strings.HasSuffix(pattern, ".*"):
		// "session.*" matches "session.state.changed" but not "sessions.updated"
		// and not the bare name "session": the dot is part of the prefix.
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	default:
		return pattern == name
	}
}
