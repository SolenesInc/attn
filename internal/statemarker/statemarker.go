// Package statemarker reads the state an agent writes into its own last
// assistant message, so a stop can be classified with no model behind it.
package statemarker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// mockAgent.mjs writes this same literal; the two must not drift.
var markerRegex = regexp.MustCompile(`<!--\s*attn:state=([A-Za-z_]+)\s*-->`)

// States are the only verdicts a marker may name. `parked` is out: classifyVerdict
// downgrades it to unknown unless the stop yielded, which a marker cannot say.
func States() []string {
	return []string{protocol.StateWaitingInput, protocol.StateIdle}
}

// Parse returns the state named by the last marker in text, or "" when the text
// carries none. A marker naming anything else fails, and the error says what.
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

// Strip removes every marker from text, so a message list never shows the
// agent's note to the classifier. Trailing whitespace the marker sat on goes too.
func Strip(text string) string {
	if !markerRegex.MatchString(text) {
		return text
	}
	return strings.TrimRight(markerRegex.ReplaceAllString(text, ""), " \t\r\n")
}
