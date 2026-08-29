// The JSON tags here ARE plugins/attn-pi/automode/config.ts's `RawAutoModeConfig`: a
// field renamed here and not there silently drops to the pi-side default.
package automode

import (
	"fmt"
	"strings"
)

type Config struct {
	EnabledDefault bool        `json:"enabled_default"`
	Environment    Environment `json:"environment"`
	Allow          []string    `json:"allow"`
	HardDeny       []string    `json:"hard_deny"`
	Models         []string    `json:"models"`
}

func Defaults() Config {
	return Config{
		EnabledDefault: true,
		Environment:    NewEnvironment(),
		Allow:          []string{},
		HardDeny:       []string{},
		Models:         []string{},
	}
}

// wsPort is the daemon's own per-profile port, so the deny names the port this machine
// actually listens on rather than a hardcoded 9849.
func ShippedHardDeny(wsPort string) []string {
	// allow, deny and model only ever write a proposal, the path an agent is
	// meant to take. env writes the config itself, so it stays blocked.
	patterns := []string{
		"*attn automode env*",
	}
	if wsPort = strings.TrimSpace(wsPort); wsPort != "" {
		patterns = append(patterns,
			"*localhost:"+wsPort+"*",
			"*127.0.0.1:"+wsPort+"*",
			"*[::1]:"+wsPort+"*",
		)
	}
	return patterns
}

// Shipped entries are resolved at read rather than written into anyone's row, so no
// stored row can drop one.
func ResolveHardDeny(wsPort string, stored []string) []string {
	resolved := ShippedHardDeny(wsPort)
	for _, pattern := range stored {
		resolved = appendUnique(resolved, pattern)
	}
	return resolved
}

// A config read, changed, and written back must not persist the shipped entries it was handed.
func StripShippedHardDeny(wsPort string, resolved []string) []string {
	shipped := map[string]bool{}
	for _, pattern := range ShippedHardDeny(wsPort) {
		shipped[pattern] = true
	}
	stored := []string{}
	for _, pattern := range resolved {
		if !shipped[pattern] {
			stored = append(stored, pattern)
		}
	}
	return stored
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Receipt: pi stops a session for a human question at 20 denials, and a denial is what prompts a proposal.
const MaxPendingProposalsPerProposer = 20

const (
	KindAllow = "allow"
	KindDeny  = "deny"
	KindModel = "model"

	TargetModels = "models"

	StatePending   = "pending"
	StatePromoted  = "promoted"
	StateDiscarded = "discarded"
)

const (
	ListAllow    = "allow"
	ListHardDeny = "hard_deny"
)

// Mirrors isBroadPattern in config.ts.
func IsBroadPattern(pattern string) bool {
	stripped := strings.Map(func(r rune) rune {
		switch r {
		case '*', '?', ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, pattern)
	return stripped == ""
}

func ValidateAllowPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("allow pattern is empty")
	}
	if IsBroadPattern(pattern) {
		return fmt.Errorf(
			"broad allow pattern %q is refused: an allow entry must name something. "+
				"A blanket allow is what the classifier exists to replace", pattern)
	}
	return nil
}

func ValidateDenyPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("deny pattern is empty")
	}
	return nil
}

func ValidatePattern(list, pattern string) error {
	switch list {
	case ListAllow:
		return ValidateAllowPattern(pattern)
	case ListHardDeny:
		return ValidateDenyPattern(pattern)
	default:
		return fmt.Errorf("unknown pattern list %q (want %s or %s)", list, ListAllow, ListHardDeny)
	}
}

const ModelListSeparator = ","

func ParseModelList(value string) ([]string, error) {
	models := []string{}
	for _, entry := range strings.Split(value, ModelListSeparator) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			return nil, fmt.Errorf("model %q is not a provider/id pair", entry)
		}
		for _, seen := range models {
			if seen == entry {
				return nil, fmt.Errorf("model %q is named twice; a pass walks each model once", entry)
			}
		}
		models = append(models, entry)
	}
	return models, nil
}

func FormatModelList(models []string) string {
	return strings.Join(models, ModelListSeparator)
}

func ValidateProposal(kind, target, value string) error {
	value = strings.TrimSpace(value)
	switch kind {
	case KindAllow:
		if target != "" {
			return fmt.Errorf("an %s proposal takes no target", kind)
		}
		return ValidateAllowPattern(value)
	case KindDeny:
		if target != "" {
			return fmt.Errorf("a %s proposal takes no target", kind)
		}
		return ValidateDenyPattern(value)
	case KindModel:
		if target != TargetModels {
			return fmt.Errorf("model target must be %q, got %q", TargetModels, target)
		}
		_, err := ParseModelList(value)
		return err
	default:
		return fmt.Errorf("unknown proposal kind %q (want %s, %s or %s)", kind, KindAllow, KindDeny, KindModel)
	}
}
