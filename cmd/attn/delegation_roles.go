package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func runDelegateRoles(args []string) {
	if len(args) == 0 || args[0] == "--json" {
		runDelegateRolesCatalog(args)
		return
	}
	command, rest := args[0], args[1:]
	var err error
	switch command {
	case "-h", "--help", "help":
		writeDelegateRolesHelp(os.Stdout)
		return
	case "show":
		err = runDelegateRolesShow(rest)
	case "history":
		err = runDelegateRolesHistory(rest)
	case "rollback":
		err = runDelegateRolesRollback(rest)
	case "apply":
		err = runDelegateRolesApply(rest)
	case "add", "set", "copy", "rm", "enable", "disable":
		err = runDelegateRolesEdit(command, rest)
	default:
		fmt.Fprintf(os.Stderr, "delegate roles: unknown command %q\n", command)
		writeDelegateRolesHelp(os.Stderr)
		os.Exit(2)
	}
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "delegate roles %s: %v\n", command, err)
	var usage usageError
	if errors.As(err, &usage) {
		os.Exit(2)
	}
	os.Exit(1)
}

type usageError struct{ error }

func usagef(format string, args ...any) error { return usageError{fmt.Errorf(format, args...)} }

func runDelegateRolesCatalog(args []string) {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: attn delegate roles [--json]")
		os.Exit(2)
	}
	result, err := client.New("").DelegationRoles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "delegate roles: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 1 {
		printJSON(result)
	} else {
		fmt.Println(prompts.DelegationRolesText(*result))
	}
}

func writeDelegateRolesHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn delegate roles [<command>]

With no command, print the roles agents choose from when delegating.
The commands below read and change the saved table in Settings > Delegation.
Every change is a new revision; rollback undoes any of them.

reading:
  show [<role>] [--json]
        the whole saved table: turned-off roles, roles that still need a
        model, alternatives and the fallback. With a role, its full guidance.
        --json prints the table as apply reads it.

  history [--limit N] [--json]
        revisions newest first, with who changed what and why.

changing (each accepts -m TEXT, the reason recorded in history):
  add <role> --name NAME [guidance] [model]
        a custom role. Its default model comes from the model flags.
  add <role> --builtin pathfinder|builder|reviewer|orchestrator [model]
        one of Attn's maintained roles (needs Add Attn roles in Settings once).
  add <role>/<alt> --when TEXT [--name NAME] [model]
        an alternative model for a role, starting from its default model.
        --when says when an agent should pick it instead of the default.

  set <role>[/<alt>] [--name NAME] [guidance] [--when TEXT] [--default] [model]
        change a role, or one of its alternatives. --default makes the
        alternative the role's default.
  set --fallback [--instructions TEXT] [model]
        the model agents use when no role fits.

  copy <role> <new-role> [--name NAME]
        an editable custom copy of a role, maintained ones included.
  rm <role>[/<alt>]
  enable|disable [<role>]
        without a role, turn the whole table on or off.

  apply <file|->
        replace the whole table with the JSON show --json printed. Refused
        when the table changed since that export.

  rollback [<revision>]
        with no revision, restore the table that was live before the current
        one; repeat to keep walking back. With a revision, restore that one,
        older or newer: rolling forward is restoring a later revision.

guidance flags (custom roles only):
  --description TEXT  --instructions TEXT  --stopping-point TEXT  --icon TEXT
  A value of @PATH reads a file, @- reads stdin.

model flags:
  --agent NAME     harness; changing it starts a fresh selection
  --model ID       "default" selects the harness default
  --provider ID    provider for a plugin harness model
  --effort LEVEL   "default" selects the harness default
  Changing the model or provider clears effort unless --effort is given.
`)
}

func runDelegateRolesShow(args []string) error {
	fs := flag.NewFlagSet("delegate roles show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the table as apply reads it")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(positionals) > 1 {
		return usagef("usage: attn delegate roles show [<role>] [--json]")
	}
	live, err := client.New("").DelegationPreferencesShow()
	if err != nil {
		return err
	}
	if len(positionals) == 1 {
		role, err := findRole(&live.Preferences, positionals[0])
		if err != nil {
			return err
		}
		if *asJSON {
			printJSON(role)
			return nil
		}
		writeDelegationRole(os.Stdout, prompts.ExpandDelegationRoles([]protocol.DelegationRole{*role})[0])
		return nil
	}
	if *asJSON {
		printJSON(live.Preferences)
		return nil
	}
	writeDelegationTable(os.Stdout, live.Preferences)
	return nil
}

func writeDelegationTable(w io.Writer, cfg protocol.DelegationPreferences) {
	offered := delegationprefs.Active(prompts.ExpandDelegationPreferences(cfg))
	state := "on"
	if !cfg.Enabled {
		state = "off: agents choose harness and model themselves"
	}
	fmt.Fprintf(w, "delegation roles %s · revision %d · %d of %d roles offered to agents\n\n", state, cfg.Revision, len(offered.Roles), len(cfg.Roles))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, role := range prompts.ExpandDelegationRoles(cfg.Roles) {
		var tags []string
		if role.Builtin != nil {
			tags = append(tags, "maintained")
		}
		if !role.Enabled {
			tags = append(tags, "off")
		} else if !slices.ContainsFunc(offered.Roles, func(r protocol.DelegationRole) bool { return r.ID == role.ID }) {
			tags = append(tags, "needs a model")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", role.ID, role.Name, delegationprefs.DescribeSelection(defaultChoice(role).Selection), strings.Join(tags, ", "))
		for _, choice := range role.Choices {
			if choice.ID != role.DefaultChoiceID {
				fmt.Fprintf(tw, "  /%s\t%s\t%s\twhen: %s\n", choice.ID, choice.Name, delegationprefs.DescribeSelection(choice.Selection), firstLine(choice.When))
			}
		}
	}
	fmt.Fprintf(tw, "--fallback\tAnything else\t%s\t\n", delegationprefs.DescribeSelection(cfg.Fallback.Selection))
	tw.Flush()
}

func writeDelegationRole(w io.Writer, role protocol.DelegationRole) {
	fmt.Fprint(w, strings.TrimSpace(fmt.Sprintf("%s %s (%s)", role.Icon, role.Name, role.ID)))
	if role.Builtin != nil {
		fmt.Fprint(w, " · maintained by Attn; copy it to edit its guidance")
	}
	if !role.Enabled {
		fmt.Fprint(w, " · off")
	}
	fmt.Fprintln(w)
	for _, section := range [][2]string{{"description", role.Description}, {"instructions", role.Instructions}, {"stopping point", role.StoppingPoint}} {
		if strings.TrimSpace(section[1]) != "" {
			fmt.Fprintf(w, "\n%s:\n%s\n", section[0], strings.TrimSpace(section[1]))
		}
	}
	fmt.Fprintf(w, "\ndefault (%s): %s\n", role.DefaultChoiceID, delegationprefs.DescribeSelection(defaultChoice(role).Selection))
	for _, choice := range role.Choices {
		if choice.ID != role.DefaultChoiceID {
			fmt.Fprintf(w, "\nalternative %s/%s %q: %s\nwhen: %s\n", role.ID, choice.ID, choice.Name, delegationprefs.DescribeSelection(choice.Selection), strings.TrimSpace(choice.When))
		}
	}
}

func runDelegateRolesHistory(args []string) error {
	fs := flag.NewFlagSet("delegate roles history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 10, "revisions to list")
	asJSON := fs.Bool("json", false, "print the revisions as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *limit <= 0 {
		return usagef("usage: attn delegate roles history [--limit N] [--json]")
	}
	history, err := client.New("").DelegationPreferencesHistory(*limit)
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(history)
		return nil
	}
	if len(history.Revisions) == 0 {
		fmt.Println("no revisions yet; the table has never been saved")
		return nil
	}
	for i, revision := range history.Revisions {
		if i > 0 {
			fmt.Println()
		}
		writeDelegationRevisionHeader(os.Stdout, revision, i == 0)
		writeDelegationChanges(os.Stdout, revision.Changes)
	}
	return nil
}

func writeDelegationRevisionHeader(w io.Writer, revision protocol.DelegationPreferencesRevision, live bool) {
	parts := []string{fmt.Sprintf("revision %d", revision.Preferences.Revision)}
	if live {
		parts[0] += " (live)"
	}
	if revision.CreatedAt != nil {
		parts = append(parts, *revision.CreatedAt)
	}
	if revision.Restores != nil {
		parts = append(parts, fmt.Sprintf("restores %d", *revision.Restores))
	}
	switch protocol.Deref(revision.Origin) {
	case protocol.DelegationPreferencesOriginSettings:
		parts = append(parts, "in Settings")
	case protocol.DelegationPreferencesOriginCli:
		parts = append(parts, "from the CLI")
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
	if revision.Message != nil {
		fmt.Fprintf(w, "  %q\n", *revision.Message)
	}
}

func writeDelegationChanges(w io.Writer, changes []string) {
	if len(changes) == 0 {
		fmt.Fprintln(w, "  no changes")
	}
	for _, change := range changes {
		fmt.Fprintln(w, "  "+change)
	}
}

func runDelegateRolesRollback(args []string) error {
	fs := flag.NewFlagSet("delegate roles rollback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "reason recorded in history")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(positionals) > 1 {
		return usagef("usage: attn delegate roles rollback [<revision>] [-m TEXT]")
	}
	var target *int
	if len(positionals) == 1 {
		revision, err := strconv.Atoi(strings.TrimPrefix(positionals[0], "r"))
		if err != nil || revision < 0 {
			return usagef("%q is not a revision; attn delegate roles history lists them", positionals[0])
		}
		target = &revision
	}
	restored, err := client.New("").DelegationPreferencesRollback(target, *message)
	if err != nil {
		return err
	}
	writeDelegationRevisionResult(os.Stdout, restored)
	return nil
}

func runDelegateRolesApply(args []string) error {
	fs := flag.NewFlagSet("delegate roles apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "reason recorded in history")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(positionals) != 1 {
		return usagef("usage: attn delegate roles apply <file|-> [-m TEXT]")
	}
	raw, err := readFileOrStdin(positionals[0])
	if err != nil {
		return err
	}
	var preferences protocol.DelegationPreferences
	if err := json.Unmarshal(raw, &preferences); err != nil {
		return fmt.Errorf("read %s: %w", positionals[0], err)
	}
	saved, err := client.New("").DelegationPreferencesCommit(preferences, *message)
	if client.ErrorCode(err) == protocol.ErrorCodeConflict {
		return fmt.Errorf("the table changed after revision %d; export it again with attn delegate roles show --json", preferences.Revision)
	}
	if err != nil {
		return err
	}
	writeDelegationRevisionResult(os.Stdout, saved)
	return nil
}

func runDelegateRolesEdit(command string, args []string) error {
	edit, message, err := parseDelegationRolesEdit(command, args, readFileOrStdin)
	if err != nil {
		return err
	}
	c := client.New("")
	live, err := c.DelegationPreferencesShow()
	if err != nil {
		return err
	}
	next := live.Preferences
	if err := edit(&next); err != nil {
		return err
	}
	saved, err := c.DelegationPreferencesCommit(next, message)
	if client.ErrorCode(err) == protocol.ErrorCodeConflict {
		return fmt.Errorf("the table changed while this command ran; run it again")
	}
	if err != nil {
		return err
	}
	writeDelegationRevisionResult(os.Stdout, saved)
	return nil
}

func writeDelegationRevisionResult(w io.Writer, revision *protocol.DelegationPreferencesRevision) {
	header := fmt.Sprintf("revision %d", revision.Preferences.Revision)
	if revision.Restores != nil {
		header += fmt.Sprintf(" restores revision %d", *revision.Restores)
	}
	fmt.Fprintln(w, header)
	writeDelegationChanges(w, revision.Changes)
	fmt.Fprintln(w, "undo: attn delegate roles rollback")
}

type delegationRolesEdit func(*protocol.DelegationPreferences) error

type optionalText struct {
	value *string
	read  func(string) ([]byte, error)
}

func (o *optionalText) String() string { return "" }

func (o *optionalText) Set(raw string) error {
	value := raw
	if path, ok := strings.CutPrefix(raw, "@"); ok && o.read != nil {
		content, err := o.read(path)
		if err != nil {
			return err
		}
		value = string(content)
	}
	o.value = &value
	return nil
}

type selectionFlags struct{ agent, model, provider, effort optionalText }

func (s selectionFlags) given() bool {
	return s.agent.value != nil || s.model.value != nil || s.provider.value != nil || s.effort.value != nil
}

func (s selectionFlags) apply(selection *protocol.DelegationSelection) {
	if v := s.agent.value; v != nil && *v != selection.Harness {
		*selection = protocol.DelegationSelection{Harness: *v}
	}
	if v := s.model.value; v != nil {
		model := harnessDefault(*v)
		if model != selection.Model {
			selection.Model, selection.Effort = model, ""
		}
	}
	if v := s.provider.value; v != nil && *v != selection.Provider {
		selection.Provider, selection.Effort = *v, ""
	}
	if selection.Model == "" {
		selection.Provider = ""
	}
	if v := s.effort.value; v != nil {
		selection.Effort = harnessDefault(*v)
	}
}

func harnessDefault(value string) string {
	if value == "default" {
		return ""
	}
	return value
}

func parseDelegationRolesEdit(command string, args []string, read func(string) ([]byte, error)) (delegationRolesEdit, string, error) {
	fs := flag.NewFlagSet("delegate roles "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	text := func(name string) *optionalText {
		value := &optionalText{read: read}
		fs.Var(value, name, "")
		return value
	}
	message := fs.String("m", "", "reason recorded in history")
	name, icon, when := text("name"), text("icon"), text("when")
	description, instructions, stoppingPoint := text("description"), text("instructions"), text("stopping-point")
	builtin := text("builtin")
	makeDefault := fs.Bool("default", false, "make the alternative the role's default")
	fallback := fs.Bool("fallback", false, "change the unmatched-work fallback")
	var model selectionFlags
	for flagName, value := range map[string]*optionalText{"agent": &model.agent, "model": &model.model, "provider": &model.provider, "effort": &model.effort} {
		fs.Var(value, flagName, "")
	}
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil {
		return nil, "", usageError{err}
	}
	guidance := guidanceEdits{name: name, icon: icon, description: description, instructions: instructions, stoppingPoint: stoppingPoint}
	target := func(want int, usage string) (string, string, error) {
		if len(positionals) != want {
			return "", "", usagef("usage: attn delegate roles %s", usage)
		}
		role, choice, alternative := strings.Cut(positionals[0], "/")
		if role == "" || (alternative && choice == "") {
			return "", "", usagef("%q is not a role or role/alternative", positionals[0])
		}
		return role, choice, nil
	}
	var edit delegationRolesEdit
	switch command {
	case "add":
		roleID, choiceID, err := target(1, "add <role>[/<alt>] ...")
		if err != nil {
			return nil, "", err
		}
		if choiceID != "" {
			edit = addAlternative(roleID, choiceID, name, when, model)
		} else {
			edit = addRole(roleID, builtin, guidance, model)
		}
	case "set":
		if *fallback {
			if len(positionals) != 0 {
				return nil, "", usagef("usage: attn delegate roles set --fallback [--instructions TEXT] [model flags]")
			}
			edit = setFallback(instructions, model)
			break
		}
		roleID, choiceID, err := target(1, "set <role>[/<alt>] ... | set --fallback ...")
		if err != nil {
			return nil, "", err
		}
		edit = setRole(roleID, choiceID, guidance, when, *makeDefault, model)
	case "copy":
		if len(positionals) != 2 {
			return nil, "", usagef("usage: attn delegate roles copy <role> <new-role> [--name NAME]")
		}
		edit = copyRole(positionals[0], positionals[1], name)
	case "rm":
		roleID, choiceID, err := target(1, "rm <role>[/<alt>]")
		if err != nil {
			return nil, "", err
		}
		edit = removeRole(roleID, choiceID)
	case "enable", "disable":
		if len(positionals) > 1 {
			return nil, "", usagef("usage: attn delegate roles %s [<role>]", command)
		}
		edit = setEnabled(positionals, command == "enable")
	}
	return edit, *message, nil
}

type guidanceEdits struct{ name, icon, description, instructions, stoppingPoint *optionalText }

func (g guidanceEdits) given() bool {
	return g.name.value != nil || g.icon.value != nil || g.description.value != nil || g.instructions.value != nil || g.stoppingPoint.value != nil
}

func (g guidanceEdits) apply(role *protocol.DelegationRole) {
	for _, field := range []struct {
		edit   *optionalText
		target *string
	}{{g.name, &role.Name}, {g.icon, &role.Icon}, {g.description, &role.Description}, {g.instructions, &role.Instructions}, {g.stoppingPoint, &role.StoppingPoint}} {
		if field.edit.value != nil {
			*field.target = strings.TrimSpace(*field.edit.value)
		}
	}
}

func addRole(roleID string, builtin *optionalText, guidance guidanceEdits, model selectionFlags) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		if _, err := findRole(cfg, roleID); err == nil {
			return fmt.Errorf("role %q already exists; change it with attn delegate roles set %s", roleID, roleID)
		}
		role := protocol.DelegationRole{ID: roleID, Enabled: true, DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Default"}}}
		if builtin.value != nil {
			if guidance.given() {
				return errors.New("a maintained role keeps Attn's guidance; add it, then copy it to edit")
			}
			if !cfg.WorkflowSkillEnabled {
				return errors.New("maintained Attn roles need the attn-workflow skill; install it once with Add Attn roles in Settings > Delegation")
			}
			kind := protocol.BuiltinDelegationRole(*builtin.value)
			role.Builtin = &kind
		} else {
			if guidance.name.value == nil {
				return usagef("a custom role needs --name (or --builtin for one of Attn's maintained roles)")
			}
			guidance.apply(&role)
		}
		model.apply(&role.Choices[0].Selection)
		cfg.Roles = append(cfg.Roles, role)
		return nil
	}
}

func addAlternative(roleID, choiceID string, name, when *optionalText, model selectionFlags) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role, err := findRole(cfg, roleID)
		if err != nil {
			return err
		}
		if _, err := findChoice(role, choiceID); err == nil {
			return fmt.Errorf("%s/%s already exists; change it with attn delegate roles set %s/%s", roleID, choiceID, roleID, choiceID)
		}
		if when.value == nil || strings.TrimSpace(*when.value) == "" {
			return usagef("an alternative needs --when: agents pick it only when its condition fits")
		}
		choice := protocol.DelegationChoice{ID: choiceID, Name: choiceID, When: strings.TrimSpace(*when.value), Selection: defaultChoice(*role).Selection}
		if name.value != nil {
			choice.Name = strings.TrimSpace(*name.value)
		}
		model.apply(&choice.Selection)
		role.Choices = append(role.Choices, choice)
		return nil
	}
}

func setRole(roleID, choiceID string, guidance guidanceEdits, when *optionalText, makeDefault bool, model selectionFlags) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role, err := findRole(cfg, roleID)
		if err != nil {
			return err
		}
		if choiceID == "" {
			if !guidance.given() && !model.given() {
				return usagef("nothing to change; pass guidance or model flags")
			}
			if role.Builtin != nil && guidance.given() {
				return fmt.Errorf("%s is maintained by Attn; attn delegate roles copy %s <new-role> makes an editable copy", roleID, roleID)
			}
			guidance.apply(role)
			choice, err := findChoice(role, role.DefaultChoiceID)
			if err != nil {
				return err
			}
			model.apply(&choice.Selection)
			return nil
		}
		choice, err := findChoice(role, choiceID)
		if err != nil {
			return err
		}
		if guidance.name.value == nil && when.value == nil && !makeDefault && !model.given() {
			return usagef("nothing to change; pass --name, --when, --default or model flags")
		}
		if guidance.name.value != nil {
			choice.Name = strings.TrimSpace(*guidance.name.value)
		}
		if when.value != nil {
			choice.When = strings.TrimSpace(*when.value)
		}
		model.apply(&choice.Selection)
		if makeDefault {
			role.DefaultChoiceID = choiceID
		}
		return nil
	}
}

func setFallback(instructions *optionalText, model selectionFlags) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		if instructions.value == nil && !model.given() {
			return usagef("nothing to change; pass --instructions or model flags")
		}
		if instructions.value != nil {
			cfg.Fallback.Instructions = strings.TrimSpace(*instructions.value)
		}
		model.apply(&cfg.Fallback.Selection)
		return nil
	}
}

func copyRole(sourceID, roleID string, name *optionalText) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		source, err := findRole(cfg, sourceID)
		if err != nil {
			return err
		}
		if _, err := findRole(cfg, roleID); err == nil {
			return fmt.Errorf("role %q already exists", roleID)
		}
		copied := prompts.ExpandDelegationRoles([]protocol.DelegationRole{*source})[0]
		copied.ID, copied.Builtin = roleID, nil
		copied.Name += " (custom)"
		if name.value != nil {
			copied.Name = strings.TrimSpace(*name.value)
		}
		copied.Choices = slices.Clone(source.Choices)
		index := slices.IndexFunc(cfg.Roles, func(r protocol.DelegationRole) bool { return r.ID == sourceID })
		cfg.Roles = slices.Insert(cfg.Roles, index+1, copied)
		return nil
	}
}

func removeRole(roleID, choiceID string) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role, err := findRole(cfg, roleID)
		if err != nil {
			return err
		}
		if choiceID == "" {
			cfg.Roles = slices.DeleteFunc(cfg.Roles, func(r protocol.DelegationRole) bool { return r.ID == roleID })
			return nil
		}
		if _, err := findChoice(role, choiceID); err != nil {
			return err
		}
		if choiceID == role.DefaultChoiceID {
			return fmt.Errorf("%s/%s is the role's default; make another alternative the default first with set %s/<alt> --default", roleID, choiceID, roleID)
		}
		role.Choices = slices.DeleteFunc(role.Choices, func(c protocol.DelegationChoice) bool { return c.ID == choiceID })
		return nil
	}
}

func setEnabled(positionals []string, enabled bool) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		if len(positionals) == 0 {
			cfg.Enabled = enabled
			return nil
		}
		role, err := findRole(cfg, positionals[0])
		if err != nil {
			return err
		}
		role.Enabled = enabled
		return nil
	}
}

func findRole(cfg *protocol.DelegationPreferences, roleID string) (*protocol.DelegationRole, error) {
	for i := range cfg.Roles {
		if cfg.Roles[i].ID == roleID {
			return &cfg.Roles[i], nil
		}
	}
	return nil, fmt.Errorf("no role %q; attn delegate roles show lists them", roleID)
}

func findChoice(role *protocol.DelegationRole, choiceID string) (*protocol.DelegationChoice, error) {
	for i := range role.Choices {
		if role.Choices[i].ID == choiceID {
			return &role.Choices[i], nil
		}
	}
	return nil, fmt.Errorf("role %q has no alternative %q", role.ID, choiceID)
}

func defaultChoice(role protocol.DelegationRole) protocol.DelegationChoice {
	for _, choice := range role.Choices {
		if choice.ID == role.DefaultChoiceID {
			return choice
		}
	}
	return protocol.DelegationChoice{}
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
