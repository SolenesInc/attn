package statemarker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

var markerRegex = regexp.MustCompile(`<!--\s*attn:state=([A-Za-z_]+)\s*-->`)

func States() []string {
	return []string{protocol.StateWaitingInput, protocol.StateIdle}
}

func Parse(text string) (string, error) {
	matches := markerRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", nil
	}
	state := strings.ToLower(matches[len(matches)-1][1])
	if !slices.Contains(States(), state) {
		return "", fmt.Errorf("state marker %q is not one of %s", state, strings.Join(States(), ", "))
	}
	return state, nil
}

func Strip(text string) string {
	if !markerRegex.MatchString(text) {
		return text
	}
	return strings.TrimRight(markerRegex.ReplaceAllString(text, ""), " \t\r\n")
}
