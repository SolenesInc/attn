package automode

import (
	"encoding/json"
	"strings"
)

type Rule struct {
	Pattern       []PatternToken `json:"pattern"`
	Decision      string         `json:"decision"`
	Justification string         `json:"justification,omitempty"`
}

type PatternToken struct {
	Alternatives []string
}

type Environment struct {
	Slots map[string][]string `json:"slots"`
	Notes []string            `json:"notes"`
}

const (
	DecisionAllow     = "allow"
	DecisionForbidden = "forbidden"

	PolicyOnRequest       = "on-request"
	SandboxWorkspaceWrite = "workspace-write"

	KindRule = "rule"
	KindHost = "host"

	StatePending   = "pending"
	StatePromoted  = "promoted"
	StateDiscarded = "discarded"
)

func NewEnvironment() Environment {
	return Environment{Slots: map[string][]string{}, Notes: []string{}}
}

func Tokens(literals ...string) []PatternToken {
	tokens := make([]PatternToken, 0, len(literals))
	for _, literal := range literals {
		tokens = append(tokens, PatternToken{Alternatives: []string{literal}})
	}
	return tokens
}

func (t PatternToken) MarshalJSON() ([]byte, error) {
	if len(t.Alternatives) == 1 {
		return json.Marshal(t.Alternatives[0])
	}
	if t.Alternatives == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal(t.Alternatives)
}

func ConvertGlob(glob, decision, justification string) (Rule, bool) {
	fields := strings.Fields(glob)
	if len(fields) == 0 {
		return Rule{}, false
	}
	if fields[len(fields)-1] == "*" {
		fields = fields[:len(fields)-1]
	}
	if len(fields) == 0 {
		return Rule{}, false
	}
	for _, field := range fields {
		if strings.ContainsAny(field, "*?") {
			return Rule{}, false
		}
	}
	rule := Rule{Pattern: Tokens(fields...), Decision: decision}
	if decision == DecisionForbidden {
		rule.Justification = justification
	}
	return rule, true
}

func StripShippedRules(resolved []Rule) []Rule {
	shipped := map[string]bool{}
	for _, literals := range [][]string{
		{"attn", "automode", "env"},
		{"attn", "automode", "rule", "remove"},
		{"attn", "automode", "host", "remove"},
		{"attn", "automode", "policy"},
	} {
		shipped[patternKey(Tokens(literals...))] = true
	}
	stored := []Rule{}
	for _, rule := range resolved {
		if !shipped[patternKey(rule.Pattern)] {
			stored = append(stored, rule)
		}
	}
	return stored
}

func patternKey(pattern []PatternToken) string {
	parts := make([]string, 0, len(pattern))
	for _, token := range pattern {
		parts = append(parts, strings.Join(token.Alternatives, "\x00"))
	}
	return strings.Join(parts, "\x01")
}
