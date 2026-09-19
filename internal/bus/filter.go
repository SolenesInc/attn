package bus

import "strings"

type Filter []string

var All = Filter{"*"}

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

func MatchPattern(pattern, name string) bool { return matchPattern(pattern, name) }

func matchPattern(pattern, name string) bool {
	switch {
	case pattern == "*" || pattern == "":
		return true
	case strings.HasSuffix(pattern, ".*"):
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	default:
		return pattern == name
	}
}
