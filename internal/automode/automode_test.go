package automode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRuleWantsTokensNotAShellLine(t *testing.T) {
	if err := ValidateRule(NormalizeRule(Rule{Pattern: Tokens("git", "push")})); err != nil {
		t.Fatalf("a two-token allow rule was refused: %v", err)
	}
	for name, rule := range map[string]Rule{
		"no tokens":     {Pattern: nil, Decision: DecisionAllow},
		"blank token":   {Pattern: Tokens("git", " "), Decision: DecisionAllow},
		"a shell line":  {Pattern: Tokens("git push"), Decision: DecisionAllow},
		"empty token":   {Pattern: []PatternToken{{}}, Decision: DecisionAllow},
		"bad decision":  {Pattern: Tokens("git"), Decision: "maybe"},
		"no justifying": {Pattern: Tokens("rm"), Decision: DecisionForbidden},
	} {
		if err := ValidateRule(rule); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	forbidden := Rule{Pattern: Tokens("rm"), Decision: DecisionForbidden, Justification: "no"}
	if err := ValidateRule(forbidden); err != nil {
		t.Errorf("a justified forbidden rule was refused: %v", err)
	}
}

func TestNormalizeRuleDefaultsToAllow(t *testing.T) {
	if got := NormalizeRule(Rule{Pattern: Tokens("ls")}).Decision; got != DecisionAllow {
		t.Errorf("decision = %q, want %q", got, DecisionAllow)
	}
}

func TestPatternTokenReadsAStringOrAlternatives(t *testing.T) {
	var rule Rule
	raw := `{"pattern":["git",["push","pull"]],"decision":"prompt"}`
	if err := json.Unmarshal([]byte(raw), &rule); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rule.Pattern) != 2 {
		t.Fatalf("pattern = %v, want two tokens", rule.Pattern)
	}
	if rule.Pattern[0].String() != "git" || rule.Pattern[1].String() != "{push|pull}" {
		t.Fatalf("tokens = %q, %q", rule.Pattern[0], rule.Pattern[1])
	}
	back, err := json.Marshal(rule.Pattern)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(back) != `["git",["push","pull"]]` {
		t.Errorf("pattern round trip = %s", back)
	}
}

// pi validates the examples; the daemon only has to carry them back unchanged.
func TestRuleCarriesMatchExamplesUntouched(t *testing.T) {
	raw := `{"pattern":["git","push"],"decision":"prompt","justification":"leaves the machine",` +
		`"match":[["git","push","origin"]],"not_match":[["git","pull"]]}`
	rule, err := ParseRuleValue(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	back, err := FormatRuleValue(rule)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if back != raw {
		t.Errorf("round trip changed the rule:\n got %s\nwant %s", back, raw)
	}
}

func TestValidateProposalReadsTheValueAsJSON(t *testing.T) {
	if err := ValidateProposal(KindRule, "", `{"pattern":["ls"]}`); err != nil {
		t.Fatalf("a rule proposal was refused: %v", err)
	}
	if err := ValidateProposal(KindHost, "", `{"host":"github.com","decision":"allow"}`); err != nil {
		t.Fatalf("a host proposal was refused: %v", err)
	}
	for name, tc := range map[string]struct{ kind, target, value string }{
		"unknown kind":   {"model", "", `{"pattern":["ls"]}`},
		"a target":       {KindRule, "models", `{"pattern":["ls"]}`},
		"not JSON":       {KindRule, "", "git push"},
		"empty pattern":  {KindRule, "", `{"pattern":[]}`},
		"host with path": {KindHost, "", `{"host":"github.com/x","decision":"allow"}`},
		"host decision":  {KindHost, "", `{"host":"github.com","decision":"prompt"}`},
	} {
		if err := ValidateProposal(tc.kind, tc.target, tc.value); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestDescribeProposalReadsAsOneLine(t *testing.T) {
	if got := DescribeProposal(KindRule, `{"pattern":["git","push"],"decision":"prompt"}`); got != "prompt git push" {
		t.Errorf("rule summary = %q", got)
	}
	if got := DescribeProposal(KindHost, `{"host":"github.com","decision":"deny"}`); got != "deny github.com" {
		t.Errorf("host summary = %q", got)
	}
}

// The JSON here IS plugins/attn-pi/approval/config.ts's RawApprovalConfig: a field renamed
// on one side without the other silently drops to the pi-side default.
func TestConfigMarshalsIntoThePiSideShape(t *testing.T) {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"enabled_default", "approval_policy", "sandbox_mode", "rules",
		"network", "environment", "legacy_patterns",
	}
	if len(fields) != len(want) {
		t.Fatalf("config has %d fields, want exactly %d: %s", len(fields), len(want), raw)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Errorf("config is missing %q: %s", name, raw)
		}
	}
	var network map[string]json.RawMessage
	if err := json.Unmarshal(fields["network"], &network); err != nil {
		t.Fatalf("network: %v", err)
	}
	for _, name := range []string{"enabled", "allowed_domains", "denied_domains"} {
		if _, ok := network[name]; !ok {
			t.Errorf("network is missing %q: %s", name, fields["network"])
		}
	}
}

func TestShippedRulesCoverAutoModesOwnWrites(t *testing.T) {
	lines := []string{}
	for _, rule := range ShippedRules() {
		if rule.Decision != DecisionForbidden {
			t.Errorf("shipped rule %q is %q, want forbidden", rule.Describe(), rule.Decision)
		}
		if strings.TrimSpace(rule.Justification) == "" {
			t.Errorf("shipped rule %q refuses without saying why", rule.Describe())
		}
		lines = append(lines, rule.Describe())
	}
	joined := strings.Join(lines, "\n")
	for _, write := range []string{
		"attn automode env", "attn automode rule remove",
		"attn automode host remove", "attn automode policy",
	} {
		if !strings.Contains(joined, write) {
			t.Errorf("shipped rules do not cover %q: %v", write, lines)
		}
	}
	for _, read := range []string{"attn automode show", "attn automode denials"} {
		if strings.Contains(joined, read) {
			t.Errorf("shipped rules cover %q, which does not write the config: %v", read, lines)
		}
	}
}

func TestResolveRulesPutsShippedFirstAndDropsDuplicates(t *testing.T) {
	shipped := ShippedRules()
	stored := []Rule{
		{Pattern: Tokens("ssh", "prod"), Decision: DecisionPrompt},
		shipped[0],
	}
	resolved := ResolveRules(stored)
	if len(resolved) != len(shipped)+1 {
		t.Fatalf("resolved = %d rules, want the shipped set plus one stored", len(resolved))
	}
	for i, rule := range shipped {
		if PatternKey(resolved[i].Pattern) != PatternKey(rule.Pattern) {
			t.Fatalf("resolved[%d] = %q, want the shipped %q", i, resolved[i].Describe(), rule.Describe())
		}
	}
	back := StripShippedRules(resolved)
	if len(back) != 1 || back[0].Describe() != "ssh prod" {
		t.Errorf("stripped = %v, want only the stored rule", back)
	}
}

func TestResolveNetworkNamesTheDaemonsOwnPort(t *testing.T) {
	shipped := ShippedDeniedDomains("29849")
	if len(shipped) != 3 || !strings.Contains(strings.Join(shipped, " "), "localhost:29849") {
		t.Fatalf("shipped denied domains = %v", shipped)
	}
	if got := ShippedDeniedDomains(""); len(got) != 0 {
		t.Errorf("no port should name no domain, got %v", got)
	}
	resolved := ResolveNetwork("29849", Network{
		Enabled:       true,
		DeniedDomains: []string{"evil.example", shipped[0]},
	})
	if len(resolved.DeniedDomains) != len(shipped)+1 {
		t.Fatalf("denied = %v, want the shipped set plus one stored", resolved.DeniedDomains)
	}
	stored := StripShippedNetwork("29849", resolved)
	if len(stored.DeniedDomains) != 1 || stored.DeniedDomains[0] != "evil.example" {
		t.Errorf("stripped denied = %v, want only the stored host", stored.DeniedDomains)
	}
}

func TestConvertGlobTakesPrefixesAndLeavesTheRest(t *testing.T) {
	for glob, want := range map[string]string{
		"rm -rf /":             "rm -rf /",
		"git push *":           "git push",
		"  gh  pr  create  ":   "gh pr create",
		"kubectl delete pod *": "kubectl delete pod",
	} {
		rule, ok := ConvertGlob(glob, DecisionAllow, "")
		if !ok {
			t.Errorf("ConvertGlob(%q) refused a convertible glob", glob)
			continue
		}
		if rule.Describe() != want {
			t.Errorf("ConvertGlob(%q) = %q, want %q", glob, rule.Describe(), want)
		}
	}
	for _, glob := range []string{"*curl*", "git status*", "*", "  ", "ssh ?rod", "* push"} {
		if _, ok := ConvertGlob(glob, DecisionAllow, ""); ok {
			t.Errorf("ConvertGlob(%q) converted a glob with a wildcard a prefix rule cannot express", glob)
		}
	}
}

func TestConvertGlobCarriesTheJustificationOnlyWhenForbidding(t *testing.T) {
	forbidden, ok := ConvertGlob("rm -rf /", DecisionForbidden, "because")
	if !ok || forbidden.Justification != "because" {
		t.Fatalf("forbidden rule = %+v", forbidden)
	}
	if err := ValidateRule(forbidden); err != nil {
		t.Fatalf("a converted forbidden rule does not validate: %v", err)
	}
	allowed, _ := ConvertGlob("rm -rf /", DecisionAllow, "because")
	if allowed.Justification != "" {
		t.Errorf("an allow rule carries a justification: %q", allowed.Justification)
	}
}

func TestValidateApprovalPolicyAndSandboxModeNameTheChoices(t *testing.T) {
	for _, policy := range Policies() {
		if err := ValidateApprovalPolicy(policy); err != nil {
			t.Errorf("policy %q refused: %v", policy, err)
		}
	}
	err := ValidateApprovalPolicy("yolo")
	if err == nil || !strings.Contains(err.Error(), PolicyOnRequest) {
		t.Errorf("error does not name the choices: %v", err)
	}
	for _, mode := range SandboxModes() {
		if err := ValidateSandboxMode(mode); err != nil {
			t.Errorf("sandbox mode %q refused: %v", mode, err)
		}
	}
	if err := ValidateSandboxMode("open"); err == nil {
		t.Error("an unknown sandbox mode was accepted")
	}
}
