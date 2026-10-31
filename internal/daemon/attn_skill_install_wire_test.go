package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheAttnSkillIsInstalledForEachAvailableAgent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	for _, root := range []string{".claude", ".agents"} {
		for _, file := range []string{"SKILL.md", filepath.Join("references", "delegation.md")} {
			if _, err := os.Stat(filepath.Join(toolHome, root, "skills", "attn", file)); err != nil {
				t.Errorf("the attn skill for %s is missing %s: %v", root, file, err)
			}
		}
	}
	settings := w.App().Initial.Settings
	for _, key := range []string{"claude_available", "codex_available"} {
		if got := settings[key]; got != "true" {
			t.Errorf("the app is told %s = %v, want true", key, got)
		}
	}
}

func TestEnabledSkillsRefreshWhenTheirSettingsAreSavedAndTheDaemonRestarts(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	preferences := *loadDelegationPreferences(app).Preferences
	preferences.WorkflowSkillEnabled = true
	preferences.Enabled = true
	added := addAttnDelegationRoles(app, preferences)
	if !added.Success {
		t.Fatal(protocol.Deref(added.Error))
	}
	cli := w.Client()
	for _, action := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"app preference save", func(t *testing.T) {
			off := *added.Preferences
			off.Enabled = false
			if result := savePreferences(app, off); !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
		}},
		{"CLI preference commit", func(t *testing.T) {
			cfg := savedPreferences(app)
			cfg.Enabled = true
			if _, err := cli.DelegationPreferencesCommit(cfg, "enable roles"); err != nil {
				t.Fatal(err)
			}
		}},
		{"CLI preference rollback", func(t *testing.T) {
			if _, err := cli.DelegationPreferencesRollback(protocol.Ptr(1), "restore roles"); err != nil {
				t.Fatal(err)
			}
		}},
		{"executable setting", func(t *testing.T) {
			setSetting(t, app, "codex_executable", os.Getenv("ATTN_CODEX_EXECUTABLE"))
		}},
		{"daemon restart", func(t *testing.T) { w.restart() }},
	} {
		t.Run(action.name, func(t *testing.T) {
			var files []string
			for _, root := range []string{".claude", ".agents"} {
				skills := []string{"attn-workflow"}
				if action.name == "executable setting" || action.name == "daemon restart" {
					skills = append(skills, "attn")
				}
				for _, skill := range skills {
					file := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), root, "skills", skill, "SKILL.md")
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
					files = append(files, file)
				}
			}
			action.run(t)
			for _, file := range files {
				if _, err := os.Stat(file); err != nil {
					t.Errorf("%s did not refresh %s: %v", action.name, file, err)
				}
			}
		})
	}
}

func TestSkillsUseTheMachineExecutableSettingAtStartup(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	executable := os.Getenv("ATTN_CLAUDE_EXECUTABLE")
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", filepath.Join(w.Dir, "unavailable-claude"))
	w.start()
	app := w.App()
	if got := app.Initial.Settings["claude_available"]; got != "false" {
		t.Fatalf("unavailable environment executable is offered: %v", got)
	}
	if _, err := w.Client().SetSetting(client.SettingsCaller{}, "claude_executable", executable); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), ".claude", "skills", "attn", "SKILL.md")
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	w.restart()
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("startup did not install the skill using the saved executable setting: %v", err)
	}
}
