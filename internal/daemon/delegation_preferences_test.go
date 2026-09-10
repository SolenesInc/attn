package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

func readPreferencesResult(t *testing.T, client *wsClient) protocol.DelegationPreferencesResultMessage {
	t.Helper()
	var result protocol.DelegationPreferencesResultMessage
	select {
	case raw := <-client.send:
		if err := json.Unmarshal(raw.payload, &result); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("missing preferences response")
	}
	return result
}

func TestDelegationPreferencesSettingsRoundTripAndConflict(t *testing.T) {
	d := newDaemonForTest(t)
	client := newInternalWSClient()
	d.handleDelegationPreferencesGet(client, &protocol.DelegationPreferencesGetMessage{RequestID: "load"})
	got := readPreferencesResult(t, client)
	if !got.Success || got.RequestID != "load" || got.Preferences == nil || got.Preferences.Enabled {
		t.Fatalf("initial settings: %+v", got)
	}
	for _, id := range []string{"pathfinder", "builder", "reviewer", "orchestrator"} {
		found := false
		for _, role := range got.Templates {
			found = found || role.ID == id
		}
		if !found {
			t.Fatalf("settings response is missing the %s preset", id)
		}
	}
	if len(got.Preferences.Roles) != 0 {
		t.Fatal("reading templates installed roles into user preferences")
	}
	cfg := *got.Preferences
	cfg.Enabled = true
	cfg.Fallback.Selection = delegationprefs.Selection{Harness: "copilot"}
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "save", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if !got.Success || got.Preferences.Revision != 1 || got.Preferences.Fallback.Selection.Model != "" {
		t.Fatalf("save: %+v", got)
	}
	if got.Preferences.WorkflowSkillEnabled || len(got.Preferences.Roles) != 0 {
		t.Fatalf("enabling guidance installed maintained roles: %+v", got.Preferences)
	}
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "stale", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if got.Success || got.Error == nil {
		t.Fatalf("stale save accepted: %+v", got)
	}
	stored, err := d.store.GetDelegationPreferences()
	if err != nil || stored.Revision != 1 {
		t.Fatalf("conflict changed settings: %+v %v", stored, err)
	}
	cfg = stored
	cfg.Enabled = false
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{RequestID: "off", Preferences: cfg})
	got = readPreferencesResult(t, client)
	if !got.Success || got.Preferences.Enabled || got.Preferences.Fallback.Selection.Harness != "copilot" {
		t.Fatalf("disable lost fallback: %+v", got)
	}
	if len(docFacts(t, d, FactDelegationPreferencesChanged)) != 2 {
		t.Fatal("expected one fact for each successful save")
	}
}

func TestAddAttnRolesInstallsWorkflowBeforeSavingReferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "dev")
	d := newDaemonForTest(t)
	d.store.SetSetting(canonicalExecutableSettingKey("codex"), os.Args[0])
	for _, harness := range []string{"claude", "copilot", "pi"} {
		d.store.SetSetting(canonicalExecutableSettingKey(harness), filepath.Join(home, "missing-"+harness))
	}
	client := newInternalWSClient()
	d.handleDelegationPreferencesGet(client, &protocol.DelegationPreferencesGetMessage{RequestID: "load"})
	loaded := readPreferencesResult(t, client)
	cfg := *loaded.Preferences
	cfg.WorkflowSkillEnabled = true
	cfg.Roles = loaded.Templates
	d.handleDelegationPreferencesSave(client, &protocol.DelegationPreferencesSaveMessage{
		RequestID: "install", Preferences: cfg, InstallWorkflowSkill: protocol.Ptr(true),
	})
	got := readPreferencesResult(t, client)
	if !got.Success || got.Preferences == nil || !got.Preferences.WorkflowSkillEnabled || len(got.Preferences.Roles) != 4 {
		t.Fatalf("install result=%+v", got)
	}
	wantPath := filepath.Join(home, ".agents", "skills", "attn-workflow")
	if len(got.WorkflowSkillPaths) != 1 || got.WorkflowSkillPaths[0] != wantPath {
		t.Fatalf("workflow paths=%v, want %s", got.WorkflowSkillPaths, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "SKILL.md")); err != nil {
		t.Fatalf("skill was not installed before save: %v", err)
	}
	for _, role := range got.Preferences.Roles {
		if role.Builtin == nil || role.Name != "" || role.Instructions != "" {
			t.Fatalf("saved maintained role copied bundled behavior: %+v", role)
		}
	}
}
