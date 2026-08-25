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

// docs/plans/2026-08-16-pi-auto-mode.md.

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
	case "allow":
		runAutoModePropose("allow", automode.KindAllow, "", args)
	case "deny":
		runAutoModePropose("deny", automode.KindDeny, "", args)
	case "model":
		runAutoModeModel(args)
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

pi's auto mode: a static safety envelope plus a classifier for everything that
reaches past it. This CLI proposes changes; only the attn app promotes one.

commands:
  show                              effective config and pending proposals
  env                               every slot, what it holds, what it means empty
  env set <slot> <value>…           replace that slot's entries
  env clear <slot>                  empty it back to its unset meaning
  env notes                         replace the prose beside the slots, from stdin
  allow <pattern>                   propose an allow entry
  deny <pattern>                    propose a hard-deny entry
  model <provider/id>…              propose the classifier's models, primary first
  model --none                      propose no model, which leaves auto mode off
  denials [--limit <n>]             recent denials, newest first

Every command takes --json.

allow, deny and model RECORD A PROPOSAL. Nothing they write changes what a
session runs under until a human promotes it in the app. A broad allow pattern —
one with no literal characters left after the wildcards — is refused outright.

The environment is a direct edit, and it is what the classifier's rules look up
about this machine: whether a destination is trusted, whether a registry is
yours, what counts as production here. A slot nobody filled means nothing is
trusted for it, which blocks more rather than less. A grant belongs in the allow
list, where a human promotes it.

Both classifier passes walk one ordered model list — the first one judges, and
the rest are tried only when the one before it cannot be reached. Name them
separated by spaces or commas; the proposal replaces the whole list.

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
	w.Flush()
	printAutoModeList("models", cfg.Models)
	printAutoModeEnvironment(cfg.Environment)
	printAutoModeList("allow", cfg.Allow)
	printAutoModeList("hard deny", cfg.HardDeny)
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
	value := p.Value
	if value == "" {
		value = "(none, which leaves auto mode off)"
	}
	if p.Target != "" {
		return p.Target + " " + value
	}
	return value
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

// printAutoModeEnvironment prints every slot, filled or not: what an empty slot
// means is the half a caller cannot guess. Detected slots show what they find.
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

func runAutoModeModel(args []string) {
	rest := stripFlags(args)
	if len(rest) == 0 && !hasFlag(args, "--none") {
		fmt.Fprintln(os.Stderr,
			"automode model: name at least one provider/id, or pass --none to propose turning auto mode off")
		os.Exit(2)
	}
	models, err := automode.ParseModelList(strings.Join(rest, automode.ModelListSeparator))
	if err != nil {
		fmt.Fprintf(os.Stderr, "automode model: %v\n", err)
		os.Exit(2)
	}
	proposeAutoModeValue("model", automode.KindModel, automode.TargetModels,
		automode.FormatModelList(models), hasFlag(args, "--json"))
}

func runAutoModePropose(verb, kind, target string, args []string) {
	proposeAutoMode(verb, kind, target, strings.Join(stripFlags(args), " "), hasFlag(args, "--json"))
}

func proposeAutoMode(verb, kind, target, value string, asJSON bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		fmt.Fprintf(os.Stderr, "automode %s: needs a value\n", verb)
		os.Exit(2)
	}
	proposeAutoModeValue(verb, kind, target, value, asJSON)
}

// The model list is the one proposal whose empty value means something: no
// model at all, which is auto mode off.
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
	return flag == "--limit"
}
