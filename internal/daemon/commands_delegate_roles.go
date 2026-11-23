package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const defaultRolesHistory = 10

func (d *Daemon) delegateRolesCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"delegate roles":         command.Typed(d.runDelegateRoles),
		"delegate roles show":    command.Typed(d.runDelegateRolesShow),
		"delegate roles history": command.Typed(d.runDelegateRolesHistory),
		"delegate roles install": command.Typed(d.runDelegateRolesInstall),
		"delegate roles apply":   command.Typed(d.runDelegateRolesApply),
		"delegate roles rollback": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesRollbackArgs) (any, error) {
			var target *int
			if a.Revision != nil {
				revision := int(*a.Revision)
				target = &revision
			}
			restored, err := d.store.RollbackDelegationPreferences(target, cliDelegationPreferencesNote(&a.Message))
			if err != nil {
				return nil, err
			}
			d.ensureEnabledWorkflowSkills()
			d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
			return delegateRolesChange(restored, nil, a.Render), nil
		}),
		"delegate roles add": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesAddArgs) (any, error) {
			roleID, choiceID, err := roleTarget(a.Target)
			if err != nil {
				return nil, err
			}
			model := roleSelection{a.Agent, a.Model, a.Effort}
			if choiceID != "" {
				return d.editDelegationRoles(a.Message, a.Render, addAlternative(roleID, choiceID, a.Name, a.When, model))
			}
			guidance := roleGuidance{a.Name, a.Icon, a.Description, a.Instructions}
			return d.editDelegationRoles(a.Message, a.Render, addRole(roleID, a.Builtin, guidance, model))
		}),
		"delegate roles set": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesSetArgs) (any, error) {
			model := roleSelection{a.Agent, a.Model, a.Effort}
			if a.Fallback {
				if a.Target != "" {
					return nil, command.Refuse("fallback_with_target", "{fallback} changes the fallback; leave out the role")
				}
				return d.editDelegationRoles(a.Message, a.Render, setFallback(a.Instructions, model))
			}
			if a.Target == "" {
				return nil, command.Usage("needs a role, or {fallback}")
			}
			roleID, choiceID, err := roleTarget(a.Target)
			if err != nil {
				return nil, err
			}
			guidance := roleGuidance{a.Name, a.Icon, a.Description, a.Instructions}
			return d.editDelegationRoles(a.Message, a.Render, setRole(roleID, choiceID, guidance, a.When, a.Default, model))
		}),
		"delegate roles copy": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesCopyArgs) (any, error) {
			return d.editDelegationRoles(a.Message, a.Render, copyRole(a.Role, a.NewRole, a.Name))
		}),
		"delegate roles rm": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesRmArgs) (any, error) {
			roleID, choiceID, err := roleTarget(a.Target)
			if err != nil {
				return nil, err
			}
			return d.editDelegationRoles(a.Message, a.Render, removeRole(roleID, choiceID))
		}),
		"delegate roles enable": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesEnableArgs) (any, error) {
			return d.editDelegationRoles(a.Message, a.Render, setEnabled(a.Role, true))
		}),
		"delegate roles disable": command.Typed(func(ctx context.Context, c command.Caller, a command.DelegateRolesDisableArgs) (any, error) {
			return d.editDelegationRoles(a.Message, a.Render, setEnabled(a.Role, false))
		}),
	}
}

func (d *Daemon) runDelegateRoles(ctx context.Context, c command.Caller, a command.DelegateRolesArgs) (any, error) {
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	active := delegationprefs.Active(prompts.ExpandDelegationPreferences(cfg))
	if len(active.Roles) > 0 || active.Fallback != nil {
		active.Guidance = prompts.DelegationRoutingGuidance()
	}
	return protocol.DelegateRolesResult{
		Revision: active.Revision, Roles: active.Roles, Fallback: active.Fallback, Guidance: active.Guidance,
		Text: rendered(a.Render, func() string { return prompts.DelegationRolesText(active) + "\n" }),
	}, nil
}

func (d *Daemon) liveDelegationPreferences() (store.DelegationPreferencesRevision, error) {
	history, err := d.store.DelegationPreferencesHistory(1)
	if err != nil || len(history) == 0 {
		return store.DelegationPreferencesRevision{Config: delegationprefs.Defaults()}, err
	}
	return history[0], nil
}

func (d *Daemon) runDelegateRolesShow(ctx context.Context, c command.Caller, a command.DelegateRolesShowArgs) (any, error) {
	live, err := d.liveDelegationPreferences()
	if err != nil {
		return nil, err
	}
	cfg := live.Config
	result := protocol.DelegateRolesShowResult{
		Enabled: cfg.Enabled, Revision: cfg.Revision, WorkflowSkillEnabled: cfg.WorkflowSkillEnabled, Roles: cfg.Roles, Fallback: cfg.Fallback,
	}
	if a.Role == "" {
		result.Text = rendered(a.Render, func() string { return delegationTableText(cfg) })
		return result, nil
	}
	role, err := findRole(&cfg, a.Role)
	if err != nil {
		return nil, err
	}
	expanded := prompts.ExpandDelegationRoles([]protocol.DelegationRole{*role})[0]
	result.Role = &expanded
	result.Text = rendered(a.Render, func() string { return delegationRoleText(expanded) })
	return result, nil
}

func (d *Daemon) runDelegateRolesHistory(ctx context.Context, c command.Caller, a command.DelegateRolesHistoryArgs) (any, error) {
	limit := defaultRolesHistory
	if a.Limit != nil {
		if *a.Limit < 1 {
			return nil, command.Usage("{limit} must be at least 1")
		}
		limit = int(*a.Limit)
	}
	history, err := d.store.DelegationPreferencesHistory(limit)
	if err != nil {
		return nil, err
	}
	result := protocol.DelegationPreferencesHistoryResult{Revisions: make([]protocol.DelegationPreferencesRevision, 0, len(history))}
	for _, revision := range history {
		result.Revisions = append(result.Revisions, delegationPreferencesRevisionWire(revision))
	}
	return protocol.DelegateRolesHistoryResult{
		Revisions: result.Revisions,
		Text:      rendered(a.Render, func() string { return delegationHistoryText(result.Revisions) }),
	}, nil
}

func (d *Daemon) runDelegateRolesInstall(ctx context.Context, c command.Caller, a command.DelegateRolesInstallArgs) (any, error) {
	live, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	next := live
	next.Enabled, next.WorkflowSkillEnabled = true, true
	for _, template := range prompts.DelegationRoleTemplates() {
		if slices.ContainsFunc(next.Roles, func(role protocol.DelegationRole) bool {
			return protocol.Deref(role.Builtin) == protocol.Deref(template.Builtin)
		}) {
			continue
		}
		stem := template.ID
		for suffix := 2; slices.ContainsFunc(next.Roles, func(role protocol.DelegationRole) bool { return role.ID == template.ID }); suffix++ {
			template.ID = fmt.Sprintf("%s-%d", stem, suffix)
		}
		next.Roles = append(next.Roles, template)
	}
	saved, paths, err := d.commitDelegationPreferences(live, next, a.Message, true)
	if err != nil {
		return nil, conflictRefusal(err)
	}
	result := delegateRolesChange(saved, paths, a.Render)
	if result.Text != nil {
		*result.Text += delegationTableText(saved.Config)
	}
	return result, nil
}

func (d *Daemon) runDelegateRolesApply(ctx context.Context, c command.Caller, a command.DelegateRolesApplyArgs) (any, error) {
	var table protocol.DelegationPreferences
	decoder := json.NewDecoder(bytes.NewReader(a.Table))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&table); err != nil {
		return nil, command.Refuse("not_a_table", "read the table: %v; the table takes the fields {attn delegate roles show --json} prints", err)
	}
	live, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	saved, _, err := d.commitDelegationPreferences(live, table, a.Message, false)
	if errors.Is(err, delegationprefs.ErrConflict) {
		return nil, command.Refuse("conflict", "the table changed after revision %d; export it again with {attn delegate roles show --json}", table.Revision)
	}
	if err != nil {
		return nil, err
	}
	return delegateRolesChange(saved, nil, a.Render), nil
}

func (d *Daemon) editDelegationRoles(message string, render bool, edit delegationRolesEdit) (any, error) {
	live, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	next := live
	if err := edit(&next); err != nil {
		return nil, err
	}
	saved, _, err := d.commitDelegationPreferences(live, next, message, false)
	if err != nil {
		return nil, conflictRefusal(err)
	}
	return delegateRolesChange(saved, nil, render), nil
}

func (d *Daemon) commitDelegationPreferences(current, next protocol.DelegationPreferences, message string, install bool) (store.DelegationPreferencesRevision, []string, error) {
	installed, err := d.installAttnRoles(current, next, install)
	if err != nil {
		return store.DelegationPreferencesRevision{}, nil, err
	}
	saved, err := d.store.SaveDelegationPreferences(next, cliDelegationPreferencesNote(&message))
	if err != nil {
		return store.DelegationPreferencesRevision{}, nil, err
	}
	if !install {
		d.ensureEnabledWorkflowSkills()
	}
	d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	return saved, installed, nil
}

func conflictRefusal(err error) error {
	if errors.Is(err, delegationprefs.ErrConflict) {
		return command.Refuse("conflict", "the table changed while this command ran; run it again")
	}
	return err
}

func delegateRolesChange(revision store.DelegationPreferencesRevision, installed []string, render bool) protocol.DelegateRolesChangeResult {
	wire := delegationPreferencesRevisionWire(revision)
	return protocol.DelegateRolesChangeResult{
		Preferences: wire.Preferences, Restores: wire.Restores, Origin: wire.Origin, Message: wire.Message, CreatedAt: wire.CreatedAt, Changes: wire.Changes,
		Installed: installed,
		Text:      rendered(render, func() string { return delegationChangeText(wire, installed) }),
	}
}

func rendered(render bool, text func() string) *string {
	if !render {
		return nil
	}
	return protocol.Ptr(text())
}

func delegationChangeText(wire protocol.DelegationPreferencesRevision, installed []string) string {
	var text strings.Builder
	for _, path := range installed {
		text.WriteString("installed " + path + "\n")
	}
	header := fmt.Sprintf("revision %d", wire.Preferences.Revision)
	if wire.Restores != nil {
		header += fmt.Sprintf(" restores revision %d", *wire.Restores)
	}
	text.WriteString(header + "\n")
	text.WriteString(delegationChangesText(wire.Changes))
	text.WriteString("undo: attn delegate roles rollback\n")
	return text.String()
}

func delegationTableText(cfg protocol.DelegationPreferences) string {
	withTableOn := prompts.ExpandDelegationPreferences(cfg)
	withTableOn.Enabled = true
	ready := delegationprefs.Active(withTableOn).Roles
	state, offered := "on", len(ready)
	if !cfg.Enabled {
		state, offered = "off: agents choose harness and model themselves", 0
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "delegation roles %s · revision %d · %d of %d roles offered to agents\n\n", state, cfg.Revision, offered, len(cfg.Roles))
	tw := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)
	for _, role := range prompts.ExpandDelegationRoles(cfg.Roles) {
		var tags []string
		if role.Builtin != nil {
			tags = append(tags, "maintained")
		}
		if !role.Enabled {
			tags = append(tags, "off")
		} else if !slices.ContainsFunc(ready, func(r protocol.DelegationRole) bool { return r.ID == role.ID }) {
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
	return out.String()
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

func delegationRoleText(role protocol.DelegationRole) string {
	var out strings.Builder
	out.WriteString(strings.TrimSpace(fmt.Sprintf("%s %s (%s)", role.Icon, role.Name, role.ID)))
	if role.Builtin != nil {
		out.WriteString(" · maintained by Attn; copy it to edit its guidance")
	}
	if !role.Enabled {
		out.WriteString(" · off")
	}
	out.WriteString("\n")
	for _, section := range [][2]string{{"description", role.Description}, {"instructions", role.Instructions}} {
		if strings.TrimSpace(section[1]) != "" {
			fmt.Fprintf(&out, "\n%s:\n%s\n", section[0], strings.TrimSpace(section[1]))
		}
	}
	fmt.Fprintf(&out, "\ndefault (%s): %s\n", role.DefaultChoiceID, delegationprefs.DescribeSelection(defaultChoice(role).Selection))
	for _, choice := range role.Choices {
		if choice.ID != role.DefaultChoiceID {
			fmt.Fprintf(&out, "\nalternative %s/%s %q: %s\nwhen: %s\n", role.ID, choice.ID, choice.Name, delegationprefs.DescribeSelection(choice.Selection), strings.TrimSpace(choice.When))
		}
	}
	return out.String()
}

func delegationHistoryText(revisions []protocol.DelegationPreferencesRevision) string {
	if len(revisions) == 0 {
		return "no revisions yet; the table has never been saved\n"
	}
	var out strings.Builder
	for i, revision := range revisions {
		if i > 0 {
			out.WriteString("\n")
		}
		parts := []string{fmt.Sprintf("revision %d", revision.Preferences.Revision)}
		if i == 0 {
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
		out.WriteString(strings.Join(parts, " · ") + "\n")
		if revision.Message != nil {
			fmt.Fprintf(&out, "  %q\n", *revision.Message)
		}
		out.WriteString(delegationChangesText(revision.Changes))
	}
	return out.String()
}

func delegationChangesText(changes []string) string {
	if len(changes) == 0 {
		return "  no changes\n"
	}
	var out strings.Builder
	for _, change := range changes {
		out.WriteString("  " + change + "\n")
	}
	return out.String()
}

type delegationRolesEdit func(*protocol.DelegationPreferences) error

type roleSelection struct{ agent, model, effort *string }

func (s roleSelection) apply(selection *protocol.DelegationSelection) {
	delegationprefs.ApplyOverrides(selection, s.agent, harnessDefault(s.model), harnessDefault(s.effort))
}

func harnessDefault(value *string) *string {
	if value != nil && *value == "default" {
		return new(string)
	}
	return value
}

type roleGuidance struct{ name, icon, description, instructions *string }

func (g roleGuidance) apply(role *protocol.DelegationRole) {
	for _, field := range []struct {
		edit   *string
		target *string
	}{{g.name, &role.Name}, {g.icon, &role.Icon}, {g.description, &role.Description}, {g.instructions, &role.Instructions}} {
		if field.edit != nil {
			*field.target = strings.TrimSpace(*field.edit)
		}
	}
}

func roleTarget(target string) (string, string, error) {
	role, choice, alternative := strings.Cut(target, "/")
	if role == "" || (alternative && choice == "") {
		return "", "", command.Usage("%q is not a role or role/alternative", target)
	}
	return role, choice, nil
}

func addRole(roleID, builtin string, guidance roleGuidance, model roleSelection) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role := protocol.DelegationRole{ID: roleID, Enabled: true, DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Default"}}}
		if builtin != "" {
			kind := protocol.BuiltinDelegationRole(builtin)
			role.Builtin = &kind
		}
		guidance.apply(&role)
		model.apply(&role.Choices[0].Selection)
		cfg.Roles = append(cfg.Roles, role)
		return nil
	}
}

func addAlternative(roleID, choiceID string, name, when *string, model roleSelection) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role, err := findRole(cfg, roleID)
		if err != nil {
			return err
		}
		choice := protocol.DelegationChoice{ID: choiceID, Name: choiceID, Selection: defaultChoice(*role).Selection}
		if name != nil {
			choice.Name = strings.TrimSpace(*name)
		}
		if when != nil {
			choice.When = strings.TrimSpace(*when)
		}
		model.apply(&choice.Selection)
		role.Choices = append(role.Choices, choice)
		return nil
	}
}

func setRole(roleID, choiceID string, guidance roleGuidance, when *string, makeDefault bool, model roleSelection) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		role, err := findRole(cfg, roleID)
		if err != nil {
			return err
		}
		if choiceID == "" {
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
		if guidance.name != nil {
			choice.Name = strings.TrimSpace(*guidance.name)
		}
		if when != nil {
			choice.When = strings.TrimSpace(*when)
		}
		model.apply(&choice.Selection)
		if makeDefault {
			role.DefaultChoiceID = choiceID
		}
		return nil
	}
}

func setFallback(instructions *string, model roleSelection) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		if instructions != nil {
			cfg.Fallback.Instructions = strings.TrimSpace(*instructions)
		}
		model.apply(&cfg.Fallback.Selection)
		return nil
	}
}

func copyRole(sourceID, roleID string, name *string) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		source, err := findRole(cfg, sourceID)
		if err != nil {
			return err
		}
		copied := prompts.ExpandDelegationRoles([]protocol.DelegationRole{*source})[0]
		copied.ID, copied.Builtin = roleID, nil
		copied.Name += " (custom)"
		if name != nil {
			copied.Name = strings.TrimSpace(*name)
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
		role.Choices = slices.DeleteFunc(role.Choices, func(c protocol.DelegationChoice) bool { return c.ID == choiceID })
		return nil
	}
}

func setEnabled(roleID string, enabled bool) delegationRolesEdit {
	return func(cfg *protocol.DelegationPreferences) error {
		if roleID == "" {
			cfg.Enabled = enabled
			return nil
		}
		role, err := findRole(cfg, roleID)
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
	return nil, command.Refuse("no_role", "no role %q; {attn delegate roles show} lists them", roleID)
}

func findChoice(role *protocol.DelegationRole, choiceID string) (*protocol.DelegationChoice, error) {
	for i := range role.Choices {
		if role.Choices[i].ID == choiceID {
			return &role.Choices[i], nil
		}
	}
	return nil, command.Refuse("no_alternative", "role %q has no alternative %q", role.ID, choiceID)
}

func defaultChoice(role protocol.DelegationRole) protocol.DelegationChoice {
	for _, choice := range role.Choices {
		if choice.ID == role.DefaultChoiceID {
			return choice
		}
	}
	return protocol.DelegationChoice{}
}
