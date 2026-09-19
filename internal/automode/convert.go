package automode

import "strings"

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
