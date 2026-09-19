package prompts

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

var delegationRoleSpecs = []struct {
	id   protocol.BuiltinDelegationRole
	name string
	icon string
}{
	{protocol.BuiltinDelegationRolePathfinder, "Pathfinder", "search"},
	{protocol.BuiltinDelegationRoleBuilder, "Builder", "code"},
	{protocol.BuiltinDelegationRoleReviewer, "Reviewer", "list"},
	{protocol.BuiltinDelegationRoleOrchestrator, "Orchestrator", "spark"},
}

func delegationPreferencesRecipient() Recipient {
	fields := func(names ...string) []Field {
		out := []Field{}
		for _, name := range names {
			out = append(out, TextField(name, name))
		}
		return out
	}
	events := []Event{}
	for _, spec := range []struct {
		id    string
		names []string
	}{
		{"guidance", nil}, {"empty", nil},
		{"role", []string{"name", "id", "description", "instructions", "stopping_point", "choices"}},
		{"choice", []string{"kind", "name", "id", "selection", "condition"}},
		{"fallback", []string{"selection", "instructions"}},
		{"execution", []string{"name", "instructions", "stopping_point"}},
		{"fallback-execution", []string{"instructions"}},
		{"opening", []string{"task", "guidance"}},
	} {
		events = append(events, On(spec.id, "message_fragment", "Delegation preference guidance supplied on demand.", template("delegation-preferences."+spec.id, "content/delegation-preferences/"+spec.id+".md", fields(spec.names...)...)))
	}
	for _, role := range delegationRoleSpecs {
		for _, field := range []string{"description", "instructions", "stopping-point"} {
			id := string(role.id) + "-" + field
			events = append(events, On(id, "message_fragment", "Editable starting guidance for a delegation role.", Use("delegation-preferences."+id, "content/delegation-preferences/"+id+".md")))
		}
	}
	return Recipient{ID: "delegation-preferences", Description: "On-demand configured roles and selected execution guidance.", Events: events}
}

func preferenceText(event string, values Values) string {
	result, err := builtin.Render("delegation-preferences", event, values)
	if err != nil {
		panic(err)
	}
	return result.Text
}

func DelegationRoleTemplates() []protocol.DelegationRole {
	result := []protocol.DelegationRole{}
	for _, item := range delegationRoleSpecs {
		builtin := item.id
		result = append(result, protocol.DelegationRole{ID: string(item.id), Builtin: &builtin, Enabled: true, DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Default"}}})
	}
	return result
}

func ExpandDelegationRoles(roles []protocol.DelegationRole) []protocol.DelegationRole {
	expanded := make([]protocol.DelegationRole, 0, len(roles))
	for _, role := range roles {
		copy := role
		if role.Builtin != nil {
			for _, spec := range delegationRoleSpecs {
				if spec.id != *role.Builtin {
					continue
				}
				key := string(spec.id)
				copy.Name = spec.name
				copy.Icon = spec.icon
				copy.Description = preferenceText(key+"-description", nil)
				copy.Instructions = preferenceText(key+"-instructions", nil)
				copy.StoppingPoint = preferenceText(key+"-stopping-point", nil)
				break
			}
		}
		expanded = append(expanded, copy)
	}
	return expanded
}

func ExpandDelegationPreferences(cfg protocol.DelegationPreferences) protocol.DelegationPreferences {
	cfg.Roles = ExpandDelegationRoles(cfg.Roles)
	return cfg
}

func DelegationRoutingGuidance() string {
	return preferenceText("guidance", nil)
}

func DelegationRolesText(result protocol.DelegationRolesResult) string {
	if len(result.Roles) == 0 && result.Fallback == nil {
		return preferenceText("empty", nil)
	}
	parts := []string{result.Guidance}
	result.Roles = ExpandDelegationRoles(result.Roles)
	selection := func(value protocol.DelegationSelection) string { raw, _ := json.Marshal(value); return string(raw) }
	for _, r := range result.Roles {
		choices := []string{}
		for _, c := range r.Choices {
			kind, condition := "Alternative", c.When
			if c.ID == r.DefaultChoiceID {
				kind = "Default"
				condition = "When no alternative fits."
			}
			choices = append(choices, preferenceText("choice", Values{"kind": kind, "name": c.Name, "id": c.ID, "selection": selection(c.Selection), "condition": condition}))
		}
		parts = append(parts, preferenceText("role", Values{"name": r.Name, "id": r.ID, "description": r.Description, "instructions": r.Instructions, "stopping_point": r.StoppingPoint, "choices": strings.Join(choices, "\n")}))
	}
	if f := result.Fallback; f != nil {
		parts = append(parts, preferenceText("fallback", Values{"selection": selection(f.Selection), "instructions": f.Instructions}))
	}
	return strings.Join(parts, "\n\n")
}

func DelegationExecutionGuidance(name, instructions, stoppingPoint string) string {
	if name == "" {
		if strings.TrimSpace(instructions) == "" {
			return ""
		}
		return preferenceText("fallback-execution", Values{"instructions": instructions})
	}
	return preferenceText("execution", Values{"name": name, "instructions": instructions, "stopping_point": stoppingPoint})
}

func DelegationOpeningWithGuidance(task, guidance string) string {
	return preferenceText("opening", Values{"task": task, "guidance": guidance})
}
