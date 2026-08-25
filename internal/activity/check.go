package activity

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxLineRunes = 90

var blockedStates = map[string]bool{
	"pending_approval": true,
	"waiting_input":    true,
	"idle":             true,
	"recoverable":      true,
}

var blockedVocabulary = []string{
	"await", "waiting", "wait", "blocked", "needs", "asks", "asked", "approval",
	"approve", "idle", "done", "finished", "complete", "ready", "paused",
	"stopped", "halted", "stuck", "pending", "requires", "question", "confirm",
	"review", "died", "crashed", "stalled",
}

type Violation struct {
	Check   string
	Message string
}

func Check(line, state string) []Violation {
	var violations []Violation
	add := func(check, format string, args ...any) {
		violations = append(violations, Violation{Check: check, Message: fmt.Sprintf(format, args...)})
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		add("nonempty", "line is empty")
		return violations
	}

	if n := utf8.RuneCountInString(trimmed); n > MaxLineRunes {
		add("length", "line is %d runes, max_line_runes=%d", n, MaxLineRunes)
	}
	if strings.Contains(trimmed, "\n") {
		add("single_line", "line contains a newline")
	}
	if strings.HasSuffix(trimmed, ".") {
		add("no_trailing_period", "line ends with a period")
	}
	if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) ||
		(strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'")) {
		add("no_quotes", "line is wrapped in quotes")
	}
	for _, stem := range []string{"the agent", "this agent", "here is", "here's", "summary", "activity", "status"} {
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, stem+":") || strings.HasPrefix(lower, stem+" is") {
			add("no_preamble", "line opens with a preamble: %q", stem)
			break
		}
	}

	if blockedStates[strings.TrimSpace(state)] {
		lower := strings.ToLower(trimmed)
		acknowledged := false
		for _, word := range blockedVocabulary {
			if strings.Contains(lower, word) {
				acknowledged = true
				break
			}
		}
		if !acknowledged {
			add("state_consistency",
				"state=%s (agent is not working) but the line does not say so", state)
		}
	}

	return violations
}
