package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheAppIsToldWhichAgentsAreAvailable(t *testing.T) {
	inPreparedBubble(t, prepareWorld(t, fakeagent.Claude, fakeagent.Codex), func(t *testing.T, w *world) {
		w.advance(0)
		settings := w.App().Initial.Settings
		for _, key := range []string{"claude_available", "codex_available"} {
			if got := settings[key]; got != "true" {
				t.Errorf("the app is told %s = %v, want true", key, got)
			}
		}
	})
}

func TestEnabledSkillsRefreshWhenTheirSettingsAreSavedAndTheDaemonRestarts(t *testing.T) {
	inPreparedBubble(t, prepareWorld(t, fakeagent.Claude, fakeagent.Codex), func(t *testing.T, w *world) {
		w.advance(0)
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
			{"daemon restart", func(t *testing.T) {
				w.restart()
				w.advance(0)
			}},
		} {
			{
				var files []string
				for _, root := range []string{".claude", ".agents"} {
					file := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), root, "skills", "attn-workflow", "SKILL.md")
					if err := os.Remove(file); err != nil {
						t.Fatalf("before %s: %v", action.name, err)
					}
					files = append(files, file)
				}
				action.run(t)
				for _, file := range files {
					if _, err := os.Stat(file); err != nil {
						t.Errorf("%s did not refresh %s: %v", action.name, file, err)
					}
				}
			}
		}
	})
}

func TestSkillsUseTheMachineExecutableSettingAtStartup(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	executable := os.Getenv("ATTN_CLAUDE_EXECUTABLE")
	t.Setenv("ATTN_CLAUDE_EXECUTABLE", filepath.Join(w.Dir, "unavailable-claude"))
	inPreparedBubble(t, w.World, func(t *testing.T, w *world) {
		w.advance(0)
		app := w.App()
		if got := app.Initial.Settings["claude_available"]; got != "false" {
			t.Fatalf("unavailable environment executable is offered: %v", got)
		}
		preferences := *loadDelegationPreferences(app).Preferences
		preferences.WorkflowSkillEnabled = true
		if added := addAttnDelegationRoles(app, preferences); !added.Success {
			t.Fatal(protocol.Deref(added.Error))
		}
		if _, err := w.Client().SetSetting(client.SettingsCaller{}, "claude_executable", executable); err != nil {
			t.Fatal(err)
		}
		skill := filepath.Join(os.Getenv("ATTN_TOOL_HOME"), ".claude", "skills", "attn-workflow", "SKILL.md")
		_ = os.Remove(skill)
		w.restart()
		w.advance(0)
		if _, err := os.Stat(skill); err != nil {
			t.Fatalf("startup did not install the skill using the saved executable setting: %v", err)
		}
	})
}
