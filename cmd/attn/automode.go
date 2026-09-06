package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runAutoMode() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeAutoModeHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "show":
		runAutoModeShow(args)
	case "env":
		runAutoModeEnv(args)
	case "rule":
		runAutoModeRule(args)
	case "host":
		runAutoModeHost(args)
	case "policy":
		runAutoModePolicy(args)
	case "denials":
		runAutoModeDenials(args)
	default:
		fmt.Fprintf(os.Stderr, "automode: unknown command %q\n", os.Args[2])
		writeAutoModeHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeAutoModeHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn automode <command>

pi's approval policy: prefix rules over the commands a session runs, an allow and
a deny list of network hosts, an approval policy and a sandbox mode. Adding a rule
or a host from here PROPOSES it; only the attn app promotes one.

commands:
  show                              effective config and pending proposals
  env                               every slot, what it holds, what it means empty
  env set <slot> <value>…           replace that slot's entries
  env clear <slot>                  empty it back to its unset meaning
  env notes                         replace the prose beside the slots, from stdin
  rule add <token>…                 propose a prefix rule over those command tokens
  rule remove <token>…              take that rule out
  host add <host>                   propose a network host rule
  host remove <host>                take that host rule out
  policy [--approval-policy P]      set the approval policy and/or the sandbox mode
         [--sandbox-mode M]
  denials [--limit <n>]             recent denials, newest first

Every command takes --json.

  --decision       for a rule: allow (the default), prompt or forbidden.
                   for a host: allow (the default) or deny.
  --justification  why a forbidden rule refuses; it is the text the agent is given.

rule add and host add RECORD A PROPOSAL. Nothing they write changes what a session
runs under until a human promotes it in the app. rule remove, host remove, policy
and env write the config itself, which is why a shipped forbidden rule stops a
session under auto mode from running them.

A rule is a command prefix, one token per argument: ` + "`rule add git push`" + ` matches
every command starting ` + "`git push`" + `. There are no wildcards.

approval policy: ` + strings.Join(automode.Policies(), ", ") + `
sandbox mode:    ` + strings.Join(automode.SandboxModes(), ", ") + `

The environment is a direct edit, and it is what the reviewer looks up about this
machine: whether a destination is trusted, whether a registry is yours, what counts
as production here. A slot nobody filled means nothing is trusted for it, which
blocks more rather than less.

`)
}

func autoModeClient() *client.Client {
	return client.New(config.SocketPath())
}

func autoModeFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "automode %s: %v\n", verb, err)
	os.Exit(1)
}

func runAutoModeShow(args []string) {
	asJSON := hasFlag(args, "--json")
	result, err := autoModeClient().AutoModeShow()
	if err != nil {
		autoModeFail("show", err)
	}
	if result == nil {
		autoModeFail("show", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	cfg := result.Config
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "enabled by default\t%t\n", cfg.EnabledDefault)
	fmt.Fprintf(w, "approval policy\t%s\n", cfg.ApprovalPolicy)
	fmt.Fprintf(w, "sandbox mode\t%s\n", cfg.SandboxMode)
	w.Flush()
	printAutoModeEnvironment(cfg.Environment)
	printAutoModeRules(cfg)
	printAutoModeList("network allowed", cfg.Network.AllowedDomains)
	printAutoModeList("network denied", cfg.Network.DeniedDomains)
	printAutoModeList("not converted (rewrite these as rules)", cfg.LegacyPatterns)
	if len(result.Proposals) == 0 {
		fmt.Println("\npending proposals: none")
		return
	}
	fmt.Printf("\npending proposals (promote them in the attn app):\n")
	pw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range result.Proposals {
		fmt.Fprintf(pw, "  %d\t%s\t%s\t%s\n", p.ID, p.Kind, autoModeProposalSubject(p), p.CreatedAt)
	}
	pw.Flush()
}

func printAutoModeList(label string, values []string) {
	if len(values) == 0 {
		fmt.Printf("\n%s: none\n", label)
		return
	}
	fmt.Printf("\n%s:\n", label)
	for i, value := range values {
		fmt.Printf("  %d  %s\n", i, value)
	}
}

func autoModeProposalSubject(p protocol.AutoModeProposalInfo) string {
	if p.Summary != "" {
		return p.Summary
	}
	return p.Value
}

func printAutoModeRules(cfg protocol.AutoModeConfigInfo) {
	if len(cfg.Rules) == 0 {
		fmt.Print("\nrules: none\n")
		return
	}
	shipped := map[string]bool{}
	for _, rule := range cfg.ShippedRules {
		shipped[autoModeRuleLine(rule)] = true
	}
	fmt.Print("\nrules:\n")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, rule := range cfg.Rules {
		line := autoModeRuleLine(rule)
		note := rule.Justification
		if shipped[line] {
			note = "built-in; " + note
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", rule.Decision, line, note)
	}
	w.Flush()
}

func autoModeRuleLine(rule protocol.AutoModeRuleInfo) string {
	tokens := make([]string, 0, len(rule.Pattern))
	for _, alternatives := range rule.Pattern {
		if len(alternatives) == 1 {
			tokens = append(tokens, alternatives[0])
			continue
		}
		tokens = append(tokens, "{"+strings.Join(alternatives, "|")+"}")
	}
	return strings.Join(tokens, " ")
}

func runAutoModeEnv(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		runAutoModeEnvList(args)
		return
	}
	switch args[0] {
	case "set":
		runAutoModeEnvSlot(args[1:], false)
	case "clear":
		runAutoModeEnvSlot(args[1:], true)
	case "notes":
		runAutoModeEnvNotes(args[1:])
	default:
		fmt.Fprintf(os.Stderr,
			"automode env: unknown command %q (want set, clear or notes)\n", args[0])
		os.Exit(2)
	}
}

func runAutoModeEnvList(args []string) {
	result, err := autoModeClient().AutoModeShow()
	if err != nil {
		autoModeFail("env", err)
	}
	if result == nil {
		autoModeFail("env", fmt.Errorf("daemon returned no result"))
	}
	if hasFlag(args, "--json") {
		writeJSON(protocol.AutoModeEnvResult{Environment: result.Config.Environment})
		return
	}
	printAutoModeEnvironment(result.Config.Environment)
}

func printAutoModeEnvironment(env protocol.AutoModeEnvironmentInfo) {
	held := map[string][]string{}
	for _, slot := range env.Slots {
		held[slot.ID] = slot.Values
	}
	detected := map[string][]string{}
	if cwd, err := os.Getwd(); err == nil {
		if slots, _ := automode.DetectFromRepo(cwd); slots != nil {
			detected = slots
		}
	}
	fmt.Println("\nenvironment:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, slot := range automode.Slots() {
		if values := held[slot.ID]; len(values) > 0 {
			fmt.Fprintf(w, "  %s\t%s\n", slot.ID, strings.Join(values, ", "))
			continue
		}
		if values := detected[slot.ID]; len(values) > 0 {
			fmt.Fprintf(w, "  %s\t(detected here: %s)\n", slot.ID, strings.Join(values, ", "))
			continue
		}
		fmt.Fprintf(w, "  %s\t(unset: %s)\n", slot.ID, slot.Unset)
	}
	w.Flush()
	if len(env.Notes) > 0 {
		fmt.Println("\nnotes (no rule reads these):")
		for _, line := range env.Notes {
			fmt.Printf("  %s\n", line)
		}
	}
}

func runAutoModeEnvSlot(args []string, clear bool) {
	asJSON := hasFlag(args, "--json")
	rest := stripFlags(args)
	verb := "env set"
	if clear {
		verb = "env clear"
	}
	if len(rest) == 0 {
		fmt.Fprintf(os.Stderr, "automode %s: needs a slot id (want one of %s)\n",
			verb, strings.Join(automode.SlotIDs(), ", "))
		os.Exit(2)
	}
	id := rest[0]
	slot, ok := automode.FindSlot(id)
	if !ok {
		fmt.Fprintf(os.Stderr, "automode %s: no slot %q (want one of %s)\n",
			verb, id, strings.Join(automode.SlotIDs(), ", "))
		os.Exit(2)
	}
	values := []string{}
	if !clear {
		for _, raw := range rest[1:] {
			for _, part := range strings.Split(raw, ",") {
				if part = strings.TrimSpace(part); part != "" {
					values = append(values, part)
				}
			}
		}
		if len(values) == 0 {
			fmt.Fprintf(os.Stderr,
				"automode env set: %s takes at least one value; `env clear %s` leaves it unset (%s)\n",
				id, id, slot.Unset)
			os.Exit(2)
		}
	}
	result, err := autoModeClient().AutoModeEnvSlot(id, values)
	if err != nil {
		autoModeFail(verb, err)
	}
	printAutoModeEnvResult(result, asJSON)
}

func runAutoModeEnvNotes(args []string) {
	asJSON := hasFlag(args, "--json")
	document, err := io.ReadAll(os.Stdin)
	if err != nil {
		autoModeFail("env notes", err)
	}
	lines := []string{}
	if trimmed := strings.TrimRight(string(document), "\n"); trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	result, err := autoModeClient().AutoModeEnvNotes(lines)
	if err != nil {
		autoModeFail("env notes", err)
	}
	printAutoModeEnvResult(result, asJSON)
}

func printAutoModeEnvResult(result *protocol.AutoModeEnvResult, asJSON bool) {
	if result == nil {
		autoModeFail("env", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	printAutoModeEnvironment(result.Environment)
}

func runAutoModeRule(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "automode rule: want `rule add <token>…` or `rule remove <token>…`")
		os.Exit(2)
	}
	rest := stripFlags(args[1:])
	switch args[0] {
	case "add":
		runAutoModeRuleAdd(rest, args)
	case "remove":
		runAutoModeRuleRemove(rest, args)
	default:
		fmt.Fprintf(os.Stderr, "automode rule: unknown command %q (want add or remove)\n", args[0])
		os.Exit(2)
	}
}

func runAutoModeRuleAdd(tokens []string, args []string) {
	if len(tokens) == 0 {
		fmt.Fprintln(os.Stderr,
			"automode rule add: name the command tokens the rule matches, such as `rule add git push`")
		os.Exit(2)
	}
	decision, _ := takeStringFlag(args, "--decision")
	justification, _ := takeStringFlag(args, "--justification")
	rule := automode.NormalizeRule(automode.Rule{
		Pattern:       automode.Tokens(tokens...),
		Decision:      strings.TrimSpace(decision),
		Justification: strings.TrimSpace(justification),
	})
	if err := automode.ValidateRule(rule); err != nil {
		fmt.Fprintf(os.Stderr, "automode rule add: %v\n", err)
		os.Exit(2)
	}
	value, err := automode.FormatRuleValue(rule)
	if err != nil {
		autoModeFail("rule add", err)
	}
	proposeAutoModeValue("rule add", automode.KindRule, "", value, hasFlag(args, "--json"))
}

func runAutoModeRuleRemove(tokens []string, args []string) {
	if len(tokens) == 0 {
		fmt.Fprintln(os.Stderr, "automode rule remove: name the command tokens the rule matches")
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModeRuleRemove(tokens)
	if err != nil {
		autoModeFail("rule remove", err)
	}
	printAutoModeConfigResult("rule remove", result, hasFlag(args, "--json"))
}

func runAutoModeHost(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "automode host: want `host add <host>` or `host remove <host>`")
		os.Exit(2)
	}
	rest := stripFlags(args[1:])
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "automode host %s: name exactly one host\n", args[0])
		os.Exit(2)
	}
	decision := automode.HostAllow
	if named, ok := takeStringFlag(args, "--decision"); ok {
		decision = strings.TrimSpace(named)
	}
	amendment := automode.HostAmendment{Host: strings.TrimSpace(rest[0]), Decision: decision}
	if err := automode.ValidateHost(amendment); err != nil {
		fmt.Fprintf(os.Stderr, "automode host %s: %v\n", args[0], err)
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		value, err := automode.FormatHostValue(amendment)
		if err != nil {
			autoModeFail("host add", err)
		}
		proposeAutoModeValue("host add", automode.KindHost, "", value, hasFlag(args, "--json"))
	case "remove":
		result, err := autoModeClient().AutoModeHostRemove(amendment.Host, amendment.Decision)
		if err != nil {
			autoModeFail("host remove", err)
		}
		printAutoModeConfigResult("host remove", result, hasFlag(args, "--json"))
	default:
		fmt.Fprintf(os.Stderr, "automode host: unknown command %q (want add or remove)\n", args[0])
		os.Exit(2)
	}
}

func runAutoModePolicy(args []string) {
	approval, _ := takeStringFlag(args, "--approval-policy")
	sandbox, _ := takeStringFlag(args, "--sandbox-mode")
	approval = strings.TrimSpace(approval)
	sandbox = strings.TrimSpace(sandbox)
	if approval == "" && sandbox == "" {
		fmt.Fprintf(os.Stderr,
			"automode policy: name --approval-policy (%s) or --sandbox-mode (%s), or both; "+
				"`automode show` reads what is set today\n",
			strings.Join(automode.Policies(), ", "), strings.Join(automode.SandboxModes(), ", "))
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModePolicySet(approval, sandbox)
	if err != nil {
		autoModeFail("policy", err)
	}
	printAutoModeConfigResult("policy", result, hasFlag(args, "--json"))
}

func printAutoModeConfigResult(verb string, result *protocol.AutoModeConfigResult, asJSON bool) {
	if result == nil {
		autoModeFail(verb, fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	cfg := result.Config
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "approval policy\t%s\n", cfg.ApprovalPolicy)
	fmt.Fprintf(w, "sandbox mode\t%s\n", cfg.SandboxMode)
	w.Flush()
	printAutoModeRules(cfg)
	printAutoModeList("network allowed", cfg.Network.AllowedDomains)
	printAutoModeList("network denied", cfg.Network.DeniedDomains)
}

func proposeAutoModeValue(verb, kind, target, value string, asJSON bool) {
	result, err := autoModeClient().AutoModePropose(kind, target, value, autoModeProposer())
	if err != nil {
		autoModeFail(verb, err)
	}
	if result == nil {
		autoModeFail(verb, fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	fmt.Printf("recorded proposal %d: %s %s\n", result.Proposal.ID, result.Proposal.Kind,
		autoModeProposalSubject(result.Proposal))
	fmt.Println("This changed nothing yet. Promote it in the attn app to put it in force.")
}

// autoModeProposer records who asked. Attribution for the human reviewing the
// list, not authorization: a proposal from anyone is equally inert.
func autoModeProposer() string {
	return strings.TrimSpace(os.Getenv("ATTN_SESSION_ID"))
}

func runAutoModeDenials(args []string) {
	asJSON := hasFlag(args, "--json")
	limit := 0
	rest := stripFlags(args)
	if value, ok := takeStringFlag(args, "--limit"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			fmt.Fprintf(os.Stderr, "automode denials: --limit wants a positive number, got %q\n", value)
			os.Exit(2)
		}
		limit = parsed
	}
	if len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "automode denials: unexpected argument %q\n", rest[0])
		os.Exit(2)
	}
	result, err := autoModeClient().AutoModeDenials(limit)
	if err != nil {
		autoModeFail("denials", err)
	}
	if result == nil {
		autoModeFail("denials", fmt.Errorf("daemon returned no result"))
	}
	if asJSON {
		writeJSON(result)
		return
	}
	writeAutoModeDenials(os.Stdout, result.Denials, protocol.Deref(result.LedgerNote))
}

func writeAutoModeDenials(out io.Writer, denials []protocol.AutoModeDenialInfo, ledgerNote string) {
	if len(denials) == 0 {
		fmt.Fprintln(out, "no denials recorded")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, denial := range denials {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				denial.CreatedAt, denial.SessionID, denial.Rule, denial.Signature, denial.Reason)
		}
		w.Flush()
	}
	if ledgerNote != "" {
		fmt.Fprintf(out, "note: %s\n", ledgerNote)
	}
}

func stripFlags(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			out = append(out, args[i])
			continue
		}
		if autoModeFlagTakesValue(args[i]) && i+1 < len(args) {
			i++
		}
	}
	return out
}

func takeStringFlag(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
	}
	return "", false
}

func autoModeFlagTakesValue(flag string) bool {
	switch flag {
	case "--limit", "--decision", "--justification", "--approval-policy", "--sandbox-mode":
		return true
	}
	return false
}
