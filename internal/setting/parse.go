package setting

import "strings"

// ParseBool reads a boolean setting's stored value: "1", "true", "yes" and "on" are true, in any case.
func ParseBool(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
