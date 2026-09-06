// The JSON tags here ARE plugins/attn-pi/approval/config.ts's `RawApprovalConfig`: a
// field renamed here and not there silently drops to the pi-side default.
package automode

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Config struct {
	EnabledDefault bool        `json:"enabled_default"`
	ApprovalPolicy string      `json:"approval_policy"`
	SandboxMode    string      `json:"sandbox_mode"`
	Rules          []Rule      `json:"rules"`
	Network        Network     `json:"network"`
	Environment    Environment `json:"environment"`
	LegacyPatterns []string    `json:"legacy_patterns"`
}

// A Codex prefix rule: the command's leading tokens, each token either a literal
// or a set of alternatives. Match and NotMatch are pi's own self-tests, carried here.
type Rule struct {
	Pattern       []PatternToken `json:"pattern"`
	Decision      string         `json:"decision"`
	Justification string         `json:"justification,omitempty"`
	Match         [][]string     `json:"match,omitempty"`
	NotMatch      [][]string     `json:"not_match,omitempty"`
}

// One token of a prefix rule. JSON reads a bare string as a single alternative and
// writes one back, so a rule the user typed round-trips as the shape they wrote.
type PatternToken struct {
	Alternatives []string
}

type Network struct {
	Enabled        bool     `json:"enabled"`
	AllowedDomains []string `json:"allowed_domains"`
	DeniedDomains  []string `json:"denied_domains"`
}

const (
	DecisionAllow     = "allow"
	DecisionPrompt    = "prompt"
	DecisionForbidden = "forbidden"

	HostAllow = "allow"
	HostDeny  = "deny"

	PolicyUntrusted = "untrusted"
	PolicyOnRequest = "on-request"
	PolicyNever     = "never"

	SandboxReadOnly         = "read-only"
	SandboxWorkspaceWrite   = "workspace-write"
	SandboxDangerFullAccess = "danger-full-access"
)

func Decisions() []string { return []string{DecisionAllow, DecisionPrompt, DecisionForbidden} }
func Policies() []string  { return []string{PolicyUntrusted, PolicyOnRequest, PolicyNever} }
func SandboxModes() []string {
	return []string{SandboxReadOnly, SandboxWorkspaceWrite, SandboxDangerFullAccess}
}

func Defaults() Config {
	return Config{
		EnabledDefault: true,
		ApprovalPolicy: PolicyOnRequest,
		SandboxMode:    SandboxWorkspaceWrite,
		Rules:          []Rule{},
		Network:        DefaultNetwork(),
		Environment:    NewEnvironment(),
		LegacyPatterns: []string{},
	}
}

func DefaultNetwork() Network {
	return Network{Enabled: true, AllowedDomains: []string{}, DeniedDomains: []string{}}
}

func Token(alternatives ...string) PatternToken {
	return PatternToken{Alternatives: append([]string{}, alternatives...)}
}

func Tokens(literals ...string) []PatternToken {
	tokens := make([]PatternToken, 0, len(literals))
	for _, literal := range literals {
		tokens = append(tokens, Token(literal))
	}
	return tokens
}

func (t PatternToken) MarshalJSON() ([]byte, error) {
	if len(t.Alternatives) == 1 {
		return json.Marshal(t.Alternatives[0])
	}
	return json.Marshal(nonNil(t.Alternatives))
}

func (t *PatternToken) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		t.Alternatives = []string{single}
		return nil
	}
	var alternatives []string
	if err := json.Unmarshal(data, &alternatives); err != nil {
		return fmt.Errorf("a rule pattern token is neither a string nor a list of strings: %s", data)
	}
	t.Alternatives = alternatives
	return nil
}

func (t PatternToken) String() string {
	if len(t.Alternatives) == 1 {
		return t.Alternatives[0]
	}
	return "{" + strings.Join(t.Alternatives, "|") + "}"
}

// PatternKey identifies a rule for removal: two rules with the same prefix are the
// same rule, whatever decision or justification they carry.
func PatternKey(pattern []PatternToken) string {
	parts := make([]string, 0, len(pattern))
	for _, token := range pattern {
		parts = append(parts, strings.Join(token.Alternatives, "\x00"))
	}
	return strings.Join(parts, "\x01")
}

func (r Rule) Describe() string {
	parts := make([]string, 0, len(r.Pattern))
	for _, token := range r.Pattern {
		parts = append(parts, token.String())
	}
	return strings.Join(parts, " ")
}

// wsPort is the daemon's own per-profile port, so the deny names the port this machine
// actually listens on rather than a hardcoded 9849.
func ShippedDeniedDomains(wsPort string) []string {
	domains := []string{}
	if wsPort = strings.TrimSpace(wsPort); wsPort != "" {
		domains = append(domains,
			"localhost:"+wsPort,
			"127.0.0.1:"+wsPort,
			"[::1]:"+wsPort,
		)
	}
	return domains
}

// rule add and host add only ever write a proposal, the path an agent is meant to
// take. These four write the config itself, so they stay forbidden.
func ShippedRules() []Rule {
	forbid := func(justification string, literals ...string) Rule {
		return Rule{
			Pattern:       Tokens(literals...),
			Decision:      DecisionForbidden,
			Justification: justification,
		}
	}
	return []Rule{
		forbid("the environment is what the reviewer reads; a session must not edit it",
			"attn", "automode", "env"),
		forbid("a session must not take away a rule it runs under",
			"attn", "automode", "rule", "remove"),
		forbid("a session must not take away a host rule it runs under",
			"attn", "automode", "host", "remove"),
		forbid("a session must not choose its own approval policy or sandbox",
			"attn", "automode", "policy"),
	}
}

// Shipped entries are resolved at read rather than written into anyone's row, so no
// stored row can drop one.
func ResolveRules(stored []Rule) []Rule {
	resolved := ShippedRules()
	shipped := shippedRuleKeys()
	for _, rule := range stored {
		if shipped[PatternKey(rule.Pattern)] {
			continue
		}
		resolved = append(resolved, rule)
	}
	return resolved
}

// A config read, changed, and written back must not persist the shipped entries it was handed.
func StripShippedRules(resolved []Rule) []Rule {
	shipped := shippedRuleKeys()
	stored := []Rule{}
	for _, rule := range resolved {
		if shipped[PatternKey(rule.Pattern)] {
			continue
		}
		stored = append(stored, rule)
	}
	return stored
}

func IsShippedRule(pattern []PatternToken) bool {
	return shippedRuleKeys()[PatternKey(pattern)]
}

func shippedRuleKeys() map[string]bool {
	keys := map[string]bool{}
	for _, rule := range ShippedRules() {
		keys[PatternKey(rule.Pattern)] = true
	}
	return keys
}

func ResolveNetwork(wsPort string, stored Network) Network {
	resolved := Network{
		Enabled:        stored.Enabled,
		AllowedDomains: nonNil(stored.AllowedDomains),
		DeniedDomains:  ShippedDeniedDomains(wsPort),
	}
	for _, domain := range stored.DeniedDomains {
		resolved.DeniedDomains = appendUnique(resolved.DeniedDomains, domain)
	}
	return resolved
}

func StripShippedNetwork(wsPort string, resolved Network) Network {
	shipped := map[string]bool{}
	for _, domain := range ShippedDeniedDomains(wsPort) {
		shipped[domain] = true
	}
	stored := Network{
		Enabled:        resolved.Enabled,
		AllowedDomains: nonNil(resolved.AllowedDomains),
		DeniedDomains:  []string{},
	}
	for _, domain := range resolved.DeniedDomains {
		if !shipped[domain] {
			stored.DeniedDomains = append(stored.DeniedDomains, domain)
		}
	}
	return stored
}

func IsShippedDomain(wsPort, domain string) bool {
	for _, shipped := range ShippedDeniedDomains(wsPort) {
		if shipped == domain {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Receipt: pi stops a session for a human question at 20 denials, and a denial is what prompts a proposal.
const MaxPendingProposalsPerProposer = 20

const (
	KindRule = "rule"
	KindHost = "host"

	StatePending   = "pending"
	StatePromoted  = "promoted"
	StateDiscarded = "discarded"
)

// One host amendment, as a proposal value and as the plugin reports it.
type HostAmendment struct {
	Host     string `json:"host"`
	Decision string `json:"decision"`
}

func ValidateRule(rule Rule) error {
	if len(rule.Pattern) == 0 {
		return fmt.Errorf("a rule needs at least one pattern token: it is the command prefix it matches")
	}
	for i, token := range rule.Pattern {
		if len(token.Alternatives) == 0 {
			return fmt.Errorf("rule pattern token %d has no alternatives", i)
		}
		for _, alternative := range token.Alternatives {
			if strings.TrimSpace(alternative) == "" {
				return fmt.Errorf("rule pattern token %d is blank", i)
			}
			if strings.ContainsAny(alternative, " \t\n\r") {
				return fmt.Errorf(
					"rule pattern token %d (%q) holds whitespace: a prefix rule takes one command "+
						"token per entry, not a shell line", i, alternative)
			}
		}
	}
	switch rule.Decision {
	case DecisionAllow, DecisionPrompt:
	case DecisionForbidden:
		if strings.TrimSpace(rule.Justification) == "" {
			return fmt.Errorf(
				"a forbidden rule needs a justification: it is the text the agent is given when %q is refused",
				rule.Describe())
		}
	default:
		return fmt.Errorf("unknown rule decision %q (want %s)", rule.Decision, strings.Join(Decisions(), ", "))
	}
	return nil
}

// NormalizeRule fills the decision a caller left out; allow is Codex's default.
func NormalizeRule(rule Rule) Rule {
	if strings.TrimSpace(rule.Decision) == "" {
		rule.Decision = DecisionAllow
	}
	rule.Decision = strings.TrimSpace(rule.Decision)
	rule.Justification = strings.TrimSpace(rule.Justification)
	return rule
}

func ValidateHost(amendment HostAmendment) error {
	host := strings.TrimSpace(amendment.Host)
	if host == "" {
		return fmt.Errorf("a host amendment needs a host")
	}
	if strings.ContainsAny(host, " \t\n\r/") {
		return fmt.Errorf("host %q is not a host name: it holds whitespace or a path", amendment.Host)
	}
	switch amendment.Decision {
	case HostAllow, HostDeny:
	default:
		return fmt.Errorf("unknown host decision %q (want %s or %s)", amendment.Decision, HostAllow, HostDeny)
	}
	return nil
}

func ValidateApprovalPolicy(policy string) error {
	for _, known := range Policies() {
		if known == policy {
			return nil
		}
	}
	return fmt.Errorf("unknown approval policy %q (want %s)", policy, strings.Join(Policies(), ", "))
}

func ValidateSandboxMode(mode string) error {
	for _, known := range SandboxModes() {
		if known == mode {
			return nil
		}
	}
	return fmt.Errorf("unknown sandbox mode %q (want %s)", mode, strings.Join(SandboxModes(), ", "))
}

func ParseRuleValue(value string) (Rule, error) {
	var rule Rule
	if err := json.Unmarshal([]byte(value), &rule); err != nil {
		return Rule{}, fmt.Errorf("a rule proposal value must be the rule's JSON: %w", err)
	}
	rule = NormalizeRule(rule)
	if err := ValidateRule(rule); err != nil {
		return Rule{}, err
	}
	return rule, nil
}

func ParseHostValue(value string) (HostAmendment, error) {
	var amendment HostAmendment
	if err := json.Unmarshal([]byte(value), &amendment); err != nil {
		return HostAmendment{}, fmt.Errorf(
			"a host proposal value must be {\"host\":…,\"decision\":…} JSON: %w", err)
	}
	amendment.Host = strings.TrimSpace(amendment.Host)
	if err := ValidateHost(amendment); err != nil {
		return HostAmendment{}, err
	}
	return amendment, nil
}

func FormatRuleValue(rule Rule) (string, error) {
	encoded, err := json.Marshal(NormalizeRule(rule))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func FormatHostValue(amendment HostAmendment) (string, error) {
	encoded, err := json.Marshal(amendment)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func ValidateProposal(kind, target, value string) error {
	if strings.TrimSpace(target) != "" {
		return fmt.Errorf("a %s proposal takes no target", kind)
	}
	switch kind {
	case KindRule:
		_, err := ParseRuleValue(value)
		return err
	case KindHost:
		_, err := ParseHostValue(value)
		return err
	default:
		return fmt.Errorf("unknown proposal kind %q (want %s or %s)", kind, KindRule, KindHost)
	}
}

// DescribeProposal is what the review list and the CLI print for one proposal.
func DescribeProposal(kind, value string) string {
	switch kind {
	case KindRule:
		if rule, err := ParseRuleValue(value); err == nil {
			return rule.Decision + " " + rule.Describe()
		}
	case KindHost:
		if amendment, err := ParseHostValue(value); err == nil {
			return amendment.Decision + " " + amendment.Host
		}
	}
	return value
}
