package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSettingsSnapshotsLeaveInstalledSkillsAlone(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	preferences := *loadDelegationPreferences(app).Preferences
	preferences.WorkflowSkillEnabled = true
	if added := addAttnDelegationRoles(app, preferences); !added.Success {
		t.Fatal(protocol.Deref(added.Error))
	}
	var files []string
	for _, root := range []string{".claude", ".agents"} {
		for _, skill := range []string{"attn", "attn-workflow"} {
			file := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), root, "skills", skill, "SKILL.md")
			if err := os.WriteFile(file, []byte("locally edited skill"), 0o644); err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		for _, file := range files {
			if content, err := os.ReadFile(file); err != nil || string(content) != "locally edited skill" {
				t.Errorf("settings snapshot changed %s (%v)", file, err)
			}
		}
	}
	t.Run("initial state", func(t *testing.T) { w.App(); unchanged(t) })
	t.Run("CLI settings", func(t *testing.T) {
		if _, err := w.Client().Settings("", true); err != nil {
			t.Fatal(err)
		}
		unchanged(t)
	})
	t.Run("app settings", func(t *testing.T) {
		app.Send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings})
		testworld.Await[protocol.SettingsUpdatedMessage](app, protocol.EventSettingsUpdated, nil)
		unchanged(t)
	})
	t.Run("settings projection", func(t *testing.T) {
		setSetting(t, app, "theme", "light")
		testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["theme"] == "light" })
		unchanged(t)
	})
	t.Run("refused setting", func(t *testing.T) {
		if refused := gardenAdvisorSetSetting(app, "theme", "invalid"); protocol.Deref(refused.Success) {
			t.Fatal("invalid theme accepted")
		}
		unchanged(t)
	})
}
