package daemon_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func buildRole() protocol.DelegationPreferences {
	return protocol.DelegationPreferences{
		Enabled: true,
		Roles: []protocol.DelegationRole{{
			ID: "build", Name: "Build", Enabled: true, Description: "Implement changes", Instructions: "Keep {{literal}} intact",
			DefaultChoiceID: "default",
			Choices:         []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "codex", Model: "test-model", Effort: "medium"}}},
		}},
		Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "copilot"}},
	}
}

func preferencesRequest(app *testworld.Peer, cmd any, id string) protocol.DelegationPreferencesResultMessage {
	app.T.Helper()
	return testworld.Request(app, cmd, protocol.EventDelegationPreferencesResult,
		func(m protocol.DelegationPreferencesResultMessage) bool { return m.RequestID == id })
}

func savePreferences(app *testworld.Peer, preferences protocol.DelegationPreferences) protocol.DelegationPreferencesResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return preferencesRequest(app, protocol.DelegationPreferencesSaveMessage{Cmd: protocol.CmdDelegationPreferencesSave, RequestID: id, Preferences: preferences}, id)
}

func savedPreferences(app *testworld.Peer) protocol.DelegationPreferences {
	app.T.Helper()
	id := uuid.NewString()
	result := preferencesRequest(app, protocol.DelegationPreferencesGetMessage{Cmd: protocol.CmdDelegationPreferencesGet, RequestID: id}, id)
	if !result.Success || result.Preferences == nil {
		app.T.Fatalf("reading delegation preferences: %s", protocol.Deref(result.Error))
	}
	return *result.Preferences
}

func TestTurningDelegationRolesOffKeepsThemAndHidesThemAcrossARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	saved := savePreferences(app, buildRole())
	if !saved.Success || saved.Preferences.Revision != 1 {
		t.Fatalf("saving a role = %+v", saved)
	}
	if roles, err := w.Client().DelegationRoles(); err != nil || len(roles.Roles) != 1 || roles.Roles[0].ID != "build" || roles.Fallback == nil {
		t.Fatalf("roles offered to agents = %+v, %v; want build and the fallback", roles, err)
	}

	off := *saved.Preferences
	off.Enabled = false
	turnedOff := savePreferences(app, off)
	if !turnedOff.Success || turnedOff.Preferences.Revision != 2 || !reflect.DeepEqual(turnedOff.Preferences.Roles, saved.Preferences.Roles) {
		t.Fatalf("turning the table off = %+v; want the roles kept at revision 2", turnedOff)
	}

	w.restart()
	if got := savedPreferences(w.App()); !reflect.DeepEqual(got, *turnedOff.Preferences) {
		t.Fatalf("after the restart the preferences are %+v, want %+v", got, *turnedOff.Preferences)
	}
	if roles, err := w.Client().DelegationRoles(); err != nil || len(roles.Roles) != 0 || roles.Fallback != nil {
		t.Fatalf("with the table off agents are offered %+v, %v; want nothing", roles, err)
	}
}

func TestADelegationPreferencesSaveThatWouldLoseAnEditIsRefused(t *testing.T) {
	w := newWorld(t)
	editor, other := w.App(), w.App()
	loaded := savedPreferences(editor)
	if first := savePreferences(other, buildRole()); !first.Success {
		t.Fatalf("the first save: %s", protocol.Deref(first.Error))
	}

	stale := buildRole()
	stale.Revision = loaded.Revision
	stale.Roles[0].Name = "Overwrite"
	if refused := savePreferences(editor, stale); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "reload") {
		t.Fatalf("a save on revision %d after another client's save = %+v, want a conflict", loaded.Revision, refused)
	}
	if got := savedPreferences(editor); got.Revision != 1 || got.Roles[0].Name != "Build" {
		t.Fatalf("after the refused save the table is %+v", got)
	}
}

func TestAnInvalidDelegationPreferencesSaveChangesNothing(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for name, mutate := range map[string]func(*protocol.DelegationPreferences){
		"a default choice that does not exist": func(c *protocol.DelegationPreferences) { c.Roles[0].DefaultChoiceID = "missing" },
		"two roles with one id":                func(c *protocol.DelegationPreferences) { c.Roles = append(c.Roles, c.Roles[0]) },
		"two choices with one id": func(c *protocol.DelegationPreferences) {
			c.Roles[0].Choices = append(c.Roles[0].Choices, c.Roles[0].Choices[0])
		},
		"a fallback model without a harness": func(c *protocol.DelegationPreferences) {
			c.Fallback.Selection = protocol.DelegationSelection{Model: "orphan"}
		},
	} {
		invalid := buildRole()
		mutate(&invalid)
		if result := savePreferences(app, invalid); result.Success {
			t.Errorf("%s was saved: %+v", name, result.Preferences)
		}
	}
	if got := savedPreferences(app); got.Revision != 0 || len(got.Roles) != 0 {
		t.Fatalf("after only invalid saves the table is %+v", got)
	}
}
