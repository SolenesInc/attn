package daemon_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAutoModeStartsOnWithCodexDefaultsAndOnlyTheShippedEntries(t *testing.T) {
	w := newWorld(t)
	cfg := autoModeConfig(t, w.cli())

	if !cfg.EnabledDefault {
		t.Error("auto mode is off on a fresh daemon")
	}
	if cfg.ApprovalPolicy != automode.PolicyOnRequest || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("policy = %q/%q, want Codex's defaults", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	if len(cfg.Environment.Slots) != 0 || len(cfg.Environment.Notes) != 0 || len(cfg.LegacyPatterns) != 0 {
		t.Errorf("a fresh config carries environment %+v and legacy patterns %v", cfg.Environment, cfg.LegacyPatterns)
	}
	if got := userRuleLines(t, cfg); len(got) != 0 {
		t.Errorf("user rules = %v, want only the shipped ones", got)
	}
	if !cfg.Network.Enabled || len(cfg.Network.AllowedDomains) != 0 {
		t.Errorf("network = %+v, want on with nothing allowed", cfg.Network)
	}
	if !slices.Equal(cfg.Network.DeniedDomains, cfg.ShippedDeniedDomains) {
		t.Errorf("denied domains = %v, want exactly the shipped %v", cfg.Network.DeniedDomains, cfg.ShippedDeniedDomains)
	}
}

func TestAutoModeEnvironmentSlotsAreTrimmedDeduplicatedAndSchemaBound(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()

	set, err := cli.AutoModeEnvSlot("domains", []string{"grafana.acme.corp", "  ", "grafana.acme.corp"})
	if err != nil {
		t.Fatalf("set the domains slot: %v", err)
	}
	if got := slotValues(set.Environment, "domains"); !slices.Equal(got, []string{"grafana.acme.corp"}) {
		t.Fatalf("domains = %v, want the one entry, trimmed and deduplicated", got)
	}
	if _, err := cli.AutoModeEnvNotes([]string{"the CI box shares this checkout"}); err != nil {
		t.Fatalf("set notes: %v", err)
	}
	if _, err := cli.AutoModeEnvSlot("not_a_slot", []string{"x"}); err == nil {
		t.Error("an unknown slot was accepted")
	}

	cfg := autoModeConfig(t, cli)
	if got := slotValues(cfg.Environment, "domains"); !slices.Equal(got, []string{"grafana.acme.corp"}) {
		t.Errorf("domains read back as %v", got)
	}
	if !slices.Equal(cfg.Environment.Notes, []string{"the CI box shares this checkout"}) {
		t.Errorf("notes read back as %v", cfg.Environment.Notes)
	}
}

func TestAutoModeProposalWaitsForTheUserAndPromotesOnce(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()

	rule := proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionPrompt, "", "git", "push"), "session-1")
	host := proposeAmendment(t, cli, automode.KindHost, hostValue(t, "github.com", automode.HostAllow), "")

	pending := autoModeShow(t, cli)
	if got := userRuleLines(t, pending.Config); len(got) != 0 {
		t.Errorf("a proposal changed the rules to %v", got)
	}
	if len(pending.Config.Network.AllowedDomains) != 0 {
		t.Errorf("a proposal changed the allowed domains to %v", pending.Config.Network.AllowedDomains)
	}
	if len(pending.Proposals) != 2 {
		t.Fatalf("pending proposals = %+v, want both", pending.Proposals)
	}
	if proposer := proposalByID(pending.Proposals, rule.ID).ProposedBy; proposer != "session-1" {
		t.Errorf("the rule proposal credits %q, want session-1", proposer)
	}

	promoted := promoteProposal(app, rule.ID)
	if !promoted.Success || promoted.Proposal.State != automode.StatePromoted || promoted.Proposal.ResolvedAt == "" {
		t.Fatalf("promote rule = %+v", promoted)
	}
	if got := userRuleLines(t, *promoted.Config); !slices.Equal(got, []string{"prompt git push"}) {
		t.Fatalf("rules after the promotion = %v", got)
	}
	if again := promoteProposal(app, rule.ID); again.Success {
		t.Error("a second promote of the same proposal was accepted")
	}
	if !promoteProposal(app, host.ID).Success {
		t.Fatal("promoting the host proposal failed")
	}

	after := autoModeShow(t, cli)
	if got := userRuleLines(t, after.Config); !slices.Equal(got, []string{"prompt git push"}) {
		t.Errorf("rules read back as %v, want the one promoted rule once", got)
	}
	if !slices.Equal(after.Config.Network.AllowedDomains, []string{"github.com"}) {
		t.Errorf("allowed domains = %v", after.Config.Network.AllowedDomains)
	}
	if len(after.Proposals) != 0 {
		t.Errorf("promoted proposals are still pending: %+v", after.Proposals)
	}
}

func TestAutoModeDiscardLeavesTheConfigAlone(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	proposal := proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "curl"), "")

	discarded := request(app, protocol.AutoModeDiscardMessage{
		Cmd: protocol.CmdAutoModeDiscard, ID: proposal.ID, RequestID: uuid.NewString(),
	}, protocol.EventAutoModeDiscardResult, func(protocol.AutoModeDiscardResultMessage) bool { return true })
	if !discarded.Success || discarded.Proposal.State != automode.StateDiscarded {
		t.Fatalf("discard = %+v", discarded)
	}
	if got := userRuleLines(t, autoModeConfig(t, cli)); len(got) != 0 {
		t.Errorf("rules = %v after a discard", got)
	}
	if promoteProposal(app, proposal.ID).Success {
		t.Error("a discarded proposal was promoted")
	}
}

func TestAutoModeRepeatedAsksDedupePerAskerAndOnePromotionAnswersThemAll(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	value := ruleValue(t, automode.DecisionAllow, "", "git", "push")

	first := proposeAmendment(t, cli, automode.KindRule, value, "session-a")
	if again := proposeAmendment(t, cli, automode.KindRule, value, "session-a"); again.ID != first.ID {
		t.Errorf("the same ask while pending recorded %d, want the existing %d", again.ID, first.ID)
	}
	other := proposeAmendment(t, cli, automode.KindRule, value, "session-b")
	if other.ID == first.ID || other.ProposedBy != "session-b" {
		t.Fatalf("session-b's ask = %+v, want its own proposal", other)
	}
	if got := len(autoModeShow(t, cli).Proposals); got != 2 {
		t.Fatalf("pending = %d, want one per asker", got)
	}

	if !promoteProposal(app, first.ID).Success {
		t.Fatal("promote failed")
	}
	if pending := autoModeShow(t, cli).Proposals; len(pending) != 0 {
		t.Errorf("pending = %+v, want the sibling ask answered by the promotion", pending)
	}

	retracted := proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionPrompt, "", "ssh", "prod"), "session-a")
	request(app, protocol.AutoModeDiscardMessage{
		Cmd: protocol.CmdAutoModeDiscard, ID: retracted.ID, RequestID: uuid.NewString(),
	}, protocol.EventAutoModeDiscardResult, func(protocol.AutoModeDiscardResultMessage) bool { return true })
	anew := proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionPrompt, "", "ssh", "prod"), "session-a")
	if anew.ID == retracted.ID {
		t.Error("asking again after a discard reused the discarded proposal")
	}
}

func TestAutoModeProposalCapNamesTheProposerTheLimitAndTheAsk(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	var ids []int
	for i := range automode.MaxPendingProposalsPerProposer {
		ids = append(ids, proposeAmendment(t, cli, automode.KindRule,
			ruleValue(t, automode.DecisionAllow, "", "curl", fmt.Sprintf("https://example.com/%d", i)), "session-a").ID)
	}
	last := ruleValue(t, automode.DecisionAllow, "", "curl", "https://example.com/last")
	_, err := cli.AutoModePropose(automode.KindRule, "", last, "session-a")
	if err == nil {
		t.Fatal("the proposal past the cap was accepted")
	}
	for _, want := range []string{"session-a", fmt.Sprint(automode.MaxPendingProposalsPerProposer), "curl https://example.com/last"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	proposeAmendment(t, cli, automode.KindRule, last, "session-b")

	request(app, protocol.AutoModeDiscardMessage{
		Cmd: protocol.CmdAutoModeDiscard, ID: ids[0], RequestID: uuid.NewString(),
	}, protocol.EventAutoModeDiscardResult, func(protocol.AutoModeDiscardResultMessage) bool { return true })
	proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "curl", "https://example.com/after"), "session-a")
}

func TestPromotingEveryAmendmentKindMovesTheConfig(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	promoteValue := func(kind, value string) protocol.AutoModeConfigInfo {
		t.Helper()
		result := promoteProposal(app, proposeAmendment(t, cli, kind, value, "session-a").ID)
		if !result.Success {
			t.Fatalf("promote %s %s: %s", kind, value, protocol.Deref(result.Error))
		}
		return *result.Config
	}

	cfg := promoteValue(automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "git", "push"))
	if got := userRuleLines(t, cfg); !slices.Equal(got, []string{"allow git push"}) {
		t.Fatalf("rules = %v after promoting a rule", got)
	}
	cfg = promoteValue(automode.KindHost, hostValue(t, "crates.io", automode.HostAllow))
	if !slices.Equal(cfg.Network.AllowedDomains, []string{"crates.io"}) {
		t.Fatalf("allowed = %v after promoting a host", cfg.Network.AllowedDomains)
	}
	pattern, err := automode.FormatPatternValue(automode.Tokens("git", "push"))
	if err != nil {
		t.Fatal(err)
	}
	cfg = promoteValue(automode.KindRuleRemove, pattern)
	if got := userRuleLines(t, cfg); len(got) != 0 {
		t.Fatalf("rules = %v after promoting a removal", got)
	}
	cfg = promoteValue(automode.KindHostRemove, hostValue(t, "crates.io", automode.HostAllow))
	if len(cfg.Network.AllowedDomains) != 0 {
		t.Fatalf("allowed = %v after promoting a host removal", cfg.Network.AllowedDomains)
	}
	policy, err := automode.FormatPolicyValue(automode.PolicyAmendment{
		ApprovalPolicy:    protocol.Ptr(automode.PolicyNever),
		AllowLocalBinding: protocol.Ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	promoteValue(automode.KindPolicy, policy)

	read := autoModeConfig(t, cli)
	if read.ApprovalPolicy != automode.PolicyNever || !read.Network.AllowLocalBinding {
		t.Fatalf("policy = %q, local binding = %t after promoting a policy", read.ApprovalPolicy, read.Network.AllowLocalBinding)
	}
	if read.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("sandbox = %q, want the field the amendment did not name held", read.SandboxMode)
	}
}

func TestAutoModeShippedEntriesStayAheadOfUserRulesAndCannotBeTakenAway(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	shipped := automode.ShippedRules()[0]

	if !promoteProposal(app, proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionPrompt, "", "ssh", "prod"), "").ID).Success {
		t.Fatal("promoting a user rule failed")
	}
	cfg := autoModeConfig(t, cli)
	if got := userRuleLines(t, cfg); !slices.Equal(got, []string{"prompt ssh prod"}) {
		t.Errorf("rules after the shipped ones = %v", got)
	}
	removeID := uuid.NewString()
	if removed := editAutoModeConfig(app, removeID, protocol.AutoModeRuleRemoveMessage{
		Cmd: protocol.CmdAutoModeRuleRemove, Pattern: [][]string{{"ssh"}, {"prod"}}, RequestID: protocol.Ptr(removeID),
	}); !removed.Success {
		t.Errorf("removing the user rule failed: %s", protocol.Deref(removed.Error))
	}

	ruleRemovalID := uuid.NewString()
	if removal := editAutoModeConfig(app, ruleRemovalID, protocol.AutoModeRuleRemoveMessage{
		Cmd: protocol.CmdAutoModeRuleRemove, Pattern: ruleInfoPattern(shipped), RequestID: protocol.Ptr(ruleRemovalID),
	}); removal.Success {
		t.Error("a shipped rule was removed")
	}
	overrideID := uuid.NewString()
	override := editAutoModeConfig(app, overrideID, protocol.AutoModeRuleAddMessage{
		Cmd: protocol.CmdAutoModeRuleAdd, Pattern: ruleLiterals(shipped), Decision: protocol.Ptr(automode.DecisionAllow), RequestID: overrideID,
	})
	if override.Success || !strings.Contains(protocol.Deref(override.Error), shipped.Describe()) {
		t.Errorf("overriding a shipped rule = %+v, want a refusal naming %q", override, shipped.Describe())
	}

	shippedPattern, err := automode.FormatPatternValue(shipped.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ShippedDeniedDomains) == 0 {
		t.Fatal("the config names no shipped denied hosts")
	}
	shippedHost := cfg.ShippedDeniedDomains[0]
	hostRemovalID := uuid.NewString()
	if removal := editAutoModeConfig(app, hostRemovalID, protocol.AutoModeHostRemoveMessage{
		Cmd: protocol.CmdAutoModeHostRemove, Host: shippedHost, Decision: automode.HostDeny, RequestID: protocol.Ptr(hostRemovalID),
	}); removal.Success {
		t.Error("a shipped denied host was removed")
	}
	overrides := map[string]string{
		automode.KindRule:       ruleValue(t, automode.DecisionAllow, "", ruleLiterals(shipped)...),
		automode.KindRuleRemove: shippedPattern,
		automode.KindHostRemove: hostValue(t, shippedHost, automode.HostDeny),
	}
	for kind, value := range overrides {
		if _, err := cli.AutoModePropose(kind, "", value, "session-a"); err == nil {
			t.Errorf("a %s proposal over a shipped entry was recorded", kind)
		}
	}

	after := autoModeConfig(t, cli)
	if got := userRuleLines(t, after); len(got) != 0 {
		t.Errorf("rules = %v, want the shipped entries alone", got)
	}
	if !slices.Equal(after.Network.DeniedDomains, after.ShippedDeniedDomains) {
		t.Errorf("denied = %v, want the shipped hosts untouched", after.Network.DeniedDomains)
	}
}

func TestAutoModeRuleEditsReplaceInPlaceAndRemoveOnlyTheNamedRule(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	add := func(decision, justification string, pattern ...string) protocol.AutoModeConfigResultMessage {
		t.Helper()
		id := uuid.NewString()
		return editAutoModeConfig(app, id, protocol.AutoModeRuleAddMessage{
			Cmd: protocol.CmdAutoModeRuleAdd, Pattern: pattern, Decision: protocol.Ptr(decision),
			Justification: protocol.Ptr(justification), RequestID: id,
		})
	}
	remove := func(pattern ...[]string) protocol.AutoModeConfigResultMessage {
		t.Helper()
		id := uuid.NewString()
		return editAutoModeConfig(app, id, protocol.AutoModeRuleRemoveMessage{
			Cmd: protocol.CmdAutoModeRuleRemove, Pattern: pattern, RequestID: protocol.Ptr(id),
		})
	}

	add(automode.DecisionAllow, "", "git", "status")
	add(automode.DecisionForbidden, "it changes real infrastructure", "terraform", "apply")
	replaced := add(automode.DecisionPrompt, "", "git", "status")
	if got := userRuleLines(t, *replaced.Config); !slices.Equal(got, []string{"prompt git status", "forbidden terraform apply"}) {
		t.Fatalf("rules = %v, want the first replaced in place", got)
	}
	if removed := remove([]string{"git"}, []string{"status"}); !slices.Equal(userRuleLines(t, *removed.Config), []string{"forbidden terraform apply"}) {
		t.Fatalf("rules after the removal = %v", userRuleLines(t, *removed.Config))
	}
	if again := remove([]string{"git"}, []string{"status"}); again.Success {
		t.Error("removing a rule that is not there was accepted")
	}

	add(automode.DecisionAllow, "", "git", "push")
	alternatives, err := automode.FormatRuleValue(automode.Rule{
		Pattern:  []automode.PatternToken{automode.Token("git"), automode.Token("push", "pull")},
		Decision: automode.DecisionPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !promoteProposal(app, proposeAmendment(t, cli, automode.KindRule, alternatives, "").ID).Success {
		t.Fatal("promoting the rule with alternatives failed")
	}
	removed := remove([]string{"git"}, []string{"push", "pull"})
	if got := userRuleLines(t, *removed.Config); !slices.Equal(got, []string{"forbidden terraform apply", "allow git push"}) {
		t.Errorf("rules = %v, want the literal namesake left", got)
	}
}

func TestAutoModeHostEditsMoveAHostBetweenLists(t *testing.T) {
	w := newWorld(t)
	app := w.app()
	host := func(cmd, decision string) protocol.AutoModeConfigResultMessage {
		t.Helper()
		id := uuid.NewString()
		if cmd == protocol.CmdAutoModeHostAdd {
			return editAutoModeConfig(app, id, protocol.AutoModeHostAddMessage{Cmd: cmd, Host: "github.com", Decision: decision, RequestID: id})
		}
		return editAutoModeConfig(app, id, protocol.AutoModeHostRemoveMessage{Cmd: cmd, Host: "github.com", Decision: decision, RequestID: protocol.Ptr(id)})
	}

	if allowed := host(protocol.CmdAutoModeHostAdd, automode.HostAllow); !slices.Equal(allowed.Config.Network.AllowedDomains, []string{"github.com"}) {
		t.Fatalf("allowed = %v", allowed.Config.Network.AllowedDomains)
	}
	denied := host(protocol.CmdAutoModeHostAdd, automode.HostDeny)
	if len(denied.Config.Network.AllowedDomains) != 0 || !slices.Contains(denied.Config.Network.DeniedDomains, "github.com") {
		t.Fatalf("network = %+v, want github.com moved to denied", denied.Config.Network)
	}
	removed := host(protocol.CmdAutoModeHostRemove, automode.HostDeny)
	if !slices.Equal(removed.Config.Network.DeniedDomains, removed.Config.ShippedDeniedDomains) {
		t.Errorf("denied = %v, want only the shipped hosts back", removed.Config.Network.DeniedDomains)
	}
	if again := host(protocol.CmdAutoModeHostRemove, automode.HostDeny); again.Success {
		t.Error("removing a host that is not there was accepted")
	}
}

func TestAutoModePolicyFieldsAndTheGuardianAreSetIndependently(t *testing.T) {
	w := newWorld(t)
	app, cli := w.app(), w.cli()
	set := func(msg protocol.AutoModePolicySetMessage) protocol.AutoModeConfigResultMessage {
		t.Helper()
		id := uuid.NewString()
		msg.Cmd, msg.RequestID = protocol.CmdAutoModePolicySet, protocol.Ptr(id)
		return editAutoModeConfig(app, id, msg)
	}
	guardian := &protocol.GuardianSelection{Provider: protocol.Ptr("provider"), Model: protocol.Ptr("review/model"), Effort: protocol.Ptr("high")}

	if cfg := set(protocol.AutoModePolicySetMessage{ApprovalPolicy: protocol.Ptr(automode.PolicyNever)}).Config; cfg.ApprovalPolicy != automode.PolicyNever || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Fatalf("policy = %q/%q, want only the approval policy changed", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	set(protocol.AutoModePolicySetMessage{Guardian: guardian})
	set(protocol.AutoModePolicySetMessage{SandboxMode: protocol.Ptr(automode.SandboxReadOnly)})
	if refused := set(protocol.AutoModePolicySetMessage{}); refused.Success {
		t.Error("a policy edit naming nothing was accepted")
	}
	if refused := set(protocol.AutoModePolicySetMessage{ApprovalPolicy: protocol.Ptr("yolo")}); refused.Success || !strings.Contains(protocol.Deref(refused.Error), automode.PolicyOnRequest) {
		t.Errorf("an unknown policy = %+v, want a refusal naming the choices", refused)
	}
	if refused := set(protocol.AutoModePolicySetMessage{Guardian: &protocol.GuardianSelection{Provider: protocol.Ptr("broken")}}); refused.Success {
		t.Error("a guardian without a model was accepted")
	}

	cfg := autoModeConfig(t, cli)
	if cfg.ApprovalPolicy != automode.PolicyNever || cfg.SandboxMode != automode.SandboxReadOnly {
		t.Errorf("policy read back as %q/%q", cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	if got := guardianOf(cfg); got != "provider review/model high" {
		t.Errorf("guardian read back as %q, want the selection kept across the other edits", got)
	}

	set(protocol.AutoModePolicySetMessage{Guardian: &protocol.GuardianSelection{}})
	if g := autoModeConfig(t, cli).Guardian; g != nil && (protocol.Deref(g.Provider) != "" || protocol.Deref(g.Model) != "") {
		t.Errorf("guardian = %s/%s after an empty selection, want it reset", protocol.Deref(g.Provider), protocol.Deref(g.Model))
	}
}

func TestAutoModeDenialLogListsNewestFirstOncePerDenialWithinItsCap(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()
	base := time.Date(2026, 8, 18, 10, 0, 0, 123_000_000, time.UTC)
	denial := func(session, action string, at time.Time) map[string]string {
		return map[string]string{
			"session_id": session, "tool": "bash", "action": action,
			"reason": "outside the envelope", "rule": "guardian", "at": at.Format(time.RFC3339Nano),
		}
	}
	ledger := []map[string]string{
		denial("pi-1", "bash curl evil.example", base),
		denial("pi-1", "bash curl evil.example", base),
		denial("pi-2", "bash curl evil.example", base),
		denial("pi-1", "write /etc/hosts", base),
		denial("pi-1", "bash curl evil.example", base.Add(time.Millisecond)),
		denial("pi-1", "bash git push --force", base.Add(time.Second)),
	}
	writeDenialLedger(t, w, ledger)

	denials, err := cli.AutoModeDenials(10)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(denials.Denials) != 5 {
		t.Fatalf("denials = %+v, want the duplicate delivery once and each distinct denial on its own", denials.Denials)
	}
	if newest := denials.Denials[0]; newest.Signature != "bash git push --force" || newest.Rule != "guardian" {
		t.Errorf("newest denial = %+v, want the last one with who decided", newest)
	}
	if limited, err := cli.AutoModeDenials(2); err != nil || len(limited.Denials) != 2 {
		t.Errorf("a limit of 2 returned %+v, %v", limited, err)
	}

	var overflow []map[string]string
	for i := range 503 {
		overflow = append(overflow, denial("pi-1", fmt.Sprintf("bash echo %d", i), base.Add(time.Hour+time.Duration(i)*time.Second)))
	}
	writeDenialLedger(t, w, append(ledger, overflow...))
	capped, err := cli.AutoModeDenials(1000)
	if err != nil {
		t.Fatalf("list denials: %v", err)
	}
	if len(capped.Denials) != 500 {
		t.Fatalf("kept %d denials, want the 500-row cap", len(capped.Denials))
	}
	if newest, oldest := capped.Denials[0].Signature, capped.Denials[499].Signature; newest != "bash echo 502" || oldest != "bash echo 3" {
		t.Errorf("kept %q through %q, want the newest 500", oldest, newest)
	}
}

func autoModeShow(t *testing.T, cli *client.Client) protocol.AutoModeShowResult {
	t.Helper()
	shown, err := cli.AutoModeShow("")
	if err != nil {
		t.Fatalf("automode show: %v", err)
	}
	return *shown
}

func autoModeConfig(t *testing.T, cli *client.Client) protocol.AutoModeConfigInfo {
	t.Helper()
	return autoModeShow(t, cli).Config
}

func userRuleLines(t *testing.T, cfg protocol.AutoModeConfigInfo) []string {
	t.Helper()
	shipped := len(cfg.ShippedRules)
	if len(cfg.Rules) < shipped {
		t.Fatalf("rules %+v do not start with the %d shipped ones", cfg.Rules, shipped)
	}
	for i, rule := range cfg.ShippedRules {
		if fmt.Sprint(cfg.Rules[i].Pattern) != fmt.Sprint(rule.Pattern) {
			t.Fatalf("rule %d is %v, want the shipped %v first", i, cfg.Rules[i].Pattern, rule.Pattern)
		}
	}
	lines := []string{}
	for _, rule := range cfg.Rules[shipped:] {
		words := []string{rule.Decision}
		for _, alternatives := range rule.Pattern {
			words = append(words, strings.Join(alternatives, "|"))
		}
		lines = append(lines, strings.Join(words, " "))
	}
	return lines
}

func proposeAmendment(t *testing.T, cli *client.Client, kind, value, proposedBy string) protocol.AutoModeProposalInfo {
	t.Helper()
	result, err := cli.AutoModePropose(kind, "", value, proposedBy)
	if err != nil {
		t.Fatalf("propose %s %s: %v", kind, value, err)
	}
	return result.Proposal
}

func promoteProposal(app *peer, id int) protocol.AutoModePromoteResultMessage {
	app.t.Helper()
	requestID := uuid.NewString()
	return request(app, protocol.AutoModePromoteMessage{Cmd: protocol.CmdAutoModePromote, ID: id, RequestID: requestID},
		protocol.EventAutoModePromoteResult, func(r protocol.AutoModePromoteResultMessage) bool { return r.RequestID == requestID })
}

func editAutoModeConfig(app *peer, requestID string, msg any) protocol.AutoModeConfigResultMessage {
	app.t.Helper()
	return request(app, msg, protocol.EventAutoModeConfigResult,
		func(r protocol.AutoModeConfigResultMessage) bool { return r.RequestID == requestID })
}

func proposalByID(proposals []protocol.AutoModeProposalInfo, id int) protocol.AutoModeProposalInfo {
	for _, p := range proposals {
		if p.ID == id {
			return p
		}
	}
	return protocol.AutoModeProposalInfo{}
}

func slotValues(env protocol.AutoModeEnvironmentInfo, id string) []string {
	for _, slot := range env.Slots {
		if slot.ID == id {
			return slot.Values
		}
	}
	return nil
}

func guardianOf(cfg protocol.AutoModeConfigInfo) string {
	if cfg.Guardian == nil {
		return ""
	}
	return protocol.Deref(cfg.Guardian.Provider) + " " + protocol.Deref(cfg.Guardian.Model) + " " + protocol.Deref(cfg.Guardian.Effort)
}

func ruleValue(t *testing.T, decision, justification string, tokens ...string) string {
	t.Helper()
	value, err := automode.FormatRuleValue(automode.Rule{Pattern: automode.Tokens(tokens...), Decision: decision, Justification: justification})
	if err != nil {
		t.Fatalf("format rule: %v", err)
	}
	return value
}

func hostValue(t *testing.T, host, decision string) string {
	t.Helper()
	value, err := automode.FormatHostValue(automode.HostAmendment{Host: host, Decision: decision})
	if err != nil {
		t.Fatalf("format host: %v", err)
	}
	return value
}

func ruleLiterals(rule automode.Rule) []string {
	out := make([]string, 0, len(rule.Pattern))
	for _, token := range rule.Pattern {
		out = append(out, token.Alternatives[0])
	}
	return out
}

func ruleInfoPattern(rule automode.Rule) [][]string {
	out := make([][]string, 0, len(rule.Pattern))
	for _, token := range rule.Pattern {
		out = append(out, token.Alternatives)
	}
	return out
}

func writeDenialLedger(t *testing.T, w *world, records []map[string]string) {
	t.Helper()
	var lines []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(append(lines, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(w.dir, automode.DenialLedgerFileName), lines, 0o600); err != nil {
		t.Fatal(err)
	}
}
