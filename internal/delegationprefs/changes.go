package delegationprefs

import (
	"fmt"
	"slices"
	"strings"
)

func Changes(before, after Config) []string {
	changes := []string{}
	if before.Enabled != after.Enabled {
		changes = append(changes, "delegation roles "+onOff(after.Enabled))
	}
	if before.WorkflowSkillEnabled != after.WorkflowSkillEnabled {
		changes = append(changes, "attn-workflow skill "+onOff(after.WorkflowSkillEnabled))
	}
	previous := map[string]Role{}
	existed := map[string]bool{}
	for _, role := range before.Roles {
		previous[role.ID] = role
		existed[role.ID] = true
	}
	current := map[string]bool{}
	for _, role := range after.Roles {
		current[role.ID] = true
		old, found := previous[role.ID]
		if !found {
			changes = append(changes, fmt.Sprintf("added role %s (%s)", role.ID, DescribeSelection(defaultSelection(role))))
			continue
		}
		changes = append(changes, roleChanges(old, role)...)
	}
	for _, role := range before.Roles {
		if !current[role.ID] {
			changes = append(changes, "removed role "+role.ID)
		}
	}
	if !slices.Equal(sharedOrder(before.Roles, roleID, current), sharedOrder(after.Roles, roleID, existed)) {
		changes = append(changes, "reordered roles")
	}
	if before.Fallback.Selection != after.Fallback.Selection {
		changes = append(changes, fmt.Sprintf("fallback: %s → %s", DescribeSelection(before.Fallback.Selection), DescribeSelection(after.Fallback.Selection)))
	}
	if before.Fallback.Instructions != after.Fallback.Instructions {
		changes = append(changes, "fallback: instructions edited")
	}
	return changes
}

func DescribeSelection(s Selection) string {
	if s.Harness == "" {
		return "no model chosen"
	}
	model := s.Model
	if s.Provider != "" {
		model = s.Provider + "/" + model
	}
	if model == "" {
		model = "default model"
	}
	parts := []string{s.Harness, model}
	if s.Effort != "" {
		parts = append(parts, s.Effort)
	}
	return strings.Join(parts, " ")
}

func roleChanges(before, after Role) []string {
	var changes []string
	add := func(format string, args ...any) {
		changes = append(changes, after.ID+": "+fmt.Sprintf(format, args...))
	}
	if before.Enabled != after.Enabled {
		add("turned %s", onOff(after.Enabled))
	}
	if (before.Builtin == nil) != (after.Builtin == nil) || (before.Builtin != nil && *before.Builtin != *after.Builtin) {
		if after.Builtin == nil {
			add("now a custom role")
		} else {
			add("now Attn's maintained %s", *after.Builtin)
		}
	}
	if before.Name != after.Name {
		add("renamed to %q", after.Name)
	}
	for _, field := range []struct{ name, before, after string }{
		{"icon", before.Icon, after.Icon},
		{"description", before.Description, after.Description},
		{"instructions", before.Instructions, after.Instructions},
		{"stopping point", before.StoppingPoint, after.StoppingPoint},
	} {
		if field.before != field.after {
			add("%s edited", field.name)
		}
	}
	if before.DefaultChoiceID != after.DefaultChoiceID {
		add("default is now %s", after.DefaultChoiceID)
	}
	previous := map[string]Choice{}
	existed := map[string]bool{}
	for _, choice := range before.Choices {
		previous[choice.ID] = choice
		existed[choice.ID] = true
	}
	current := map[string]bool{}
	for _, choice := range after.Choices {
		current[choice.ID] = true
		label := after.ID + "/" + choice.ID
		if choice.ID == after.DefaultChoiceID && choice.ID == before.DefaultChoiceID {
			label = after.ID
		}
		old, existed := previous[choice.ID]
		if !existed {
			add("added alternative %s (%s)", choice.ID, DescribeSelection(choice.Selection))
			continue
		}
		if old.Selection != choice.Selection {
			changes = append(changes, fmt.Sprintf("%s: %s → %s", label, DescribeSelection(old.Selection), DescribeSelection(choice.Selection)))
		}
		if old.Name != choice.Name {
			changes = append(changes, fmt.Sprintf("%s: renamed to %q", label, choice.Name))
		}
		if old.When != choice.When {
			changes = append(changes, label+": condition edited")
		}
	}
	for _, choice := range before.Choices {
		if !current[choice.ID] {
			add("removed alternative %s", choice.ID)
		}
	}
	if !slices.Equal(sharedOrder(before.Choices, choiceID, current), sharedOrder(after.Choices, choiceID, existed)) {
		add("reordered alternatives")
	}
	return changes
}

func defaultSelection(role Role) Selection {
	for _, choice := range role.Choices {
		if choice.ID == role.DefaultChoiceID {
			return choice.Selection
		}
	}
	return Selection{}
}

func sharedOrder[T any](items []T, id func(T) string, keep map[string]bool) []string {
	var ids []string
	for _, item := range items {
		if keep[id(item)] {
			ids = append(ids, id(item))
		}
	}
	return ids
}

func roleID(role Role) string { return role.ID }

func choiceID(choice Choice) string { return choice.ID }

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
