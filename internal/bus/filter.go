package bus

import "strings"

type Filter []string

var All = Filter{"*"}

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
