package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

var attnSkillRoots = map[fakeagent.Harness]string{
	fakeagent.Claude: ".claude",
	fakeagent.Codex:  ".agents",
}

func installedAttnSkill(toolHome string, h fakeagent.Harness) string {
	return filepath.Join(toolHome, attnSkillRoots[h], "skills", "attn")
}

func plantFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func skillAnEarlierVersionInstalled(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{"SKILL.md", "references/delegation.md"} {
		contents, err := os.ReadFile(filepath.Join("testdata", "retired-attn-skill", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(contents)
	}
	return files
}

func startWithCodexUnavailable(t *testing.T, plant func(toolHome string), check func(t *testing.T, toolHome string)) {
	t.Setenv("ATTN_INSTANCE", "")
	prepared := prepareWorld(t, fakeagent.Claude)
	t.Setenv("ATTN_CODEX_EXECUTABLE", filepath.Join(prepared.Dir, "codex-missing"))
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	plant(toolHome)
	inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
		w.advance(0)
		if got := w.App().Initial.Settings["codex_available"]; got != "false" {
			t.Fatalf("codex_available = %q, want false", got)
		}
		check(t, toolHome)
	})
}

func TestStartingAttnRemovesOnlyTheSkillFilesAnEarlierVersionInstalled(t *testing.T) {
	installed := skillAnEarlierVersionInstalled(t)
	userNote := "Notes I keep next to the attn skill.\n"
	startWithCodexUnavailable(t, func(toolHome string) {
		plantFiles(t, installedAttnSkill(toolHome, fakeagent.Claude), installed)
		plantFiles(t, installedAttnSkill(toolHome, fakeagent.Codex), installed)
		plantFiles(t, installedAttnSkill(toolHome, fakeagent.Codex), map[string]string{"references/my-notes.md": userNote})
	}, func(t *testing.T, toolHome string) {
		if _, err := os.Stat(installedAttnSkill(toolHome, fakeagent.Claude)); !os.IsNotExist(err) {
			t.Errorf("claude still has the attn skill an earlier version installed (%v)", err)
		}
		codexSkill := installedAttnSkill(toolHome, fakeagent.Codex)
		for name := range installed {
			if _, err := os.Stat(filepath.Join(codexSkill, filepath.FromSlash(name))); !os.IsNotExist(err) {
				t.Errorf("codex still has %s from the attn skill an earlier version installed (%v)", name, err)
			}
		}
		if got, err := os.ReadFile(filepath.Join(codexSkill, "references", "my-notes.md")); err != nil || string(got) != userNote {
			t.Errorf("attn removed or changed the user's note beside the old skill: %q, %v", got, err)
		}
	})
}

func TestStartingAttnKeepsASkillTheUserNamedAttn(t *testing.T) {
	userSkill := map[string]string{
		"SKILL.md":               "---\nname: attn\ndescription: My personal attention notes\n---\n\n# My notes\n",
		"references/my-notes.md": "My original notes, not attn's bundled skill.\n",
	}
	startWithCodexUnavailable(t, func(toolHome string) {
		for h := range attnSkillRoots {
			plantFiles(t, installedAttnSkill(toolHome, h), userSkill)
		}
	}, func(t *testing.T, toolHome string) {
		for h := range attnSkillRoots {
			for name, contents := range userSkill {
				if got, err := os.ReadFile(filepath.Join(installedAttnSkill(toolHome, h), filepath.FromSlash(name))); err != nil || string(got) != contents {
					t.Errorf("attn removed or changed the user's %s skill file %s: %q, %v", h, name, got, err)
				}
			}
		}
	})
}

func TestAVerificationInstanceLeavesTheUsersSkillsAloneUnlessItsHarnessOwnsTheToolHome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		installs bool
	}{
		{"a verification instance", map[string]string{"ATTN_INSTANCE": "fixture-lab"}, false},
		{"the dev instance", map[string]string{"ATTN_INSTANCE": "dev"}, true},
		{"a scenario harness with its own tool home", map[string]string{"ATTN_INSTANCE": "fixture-lab", "ATTN_AUTOMATION": "1", "ATTN_HARNESS_SKILL_SYNC": "1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
			toolHome := os.Getenv("ATTN_TOOL_HOME")
			app := w.App()
			roles := *loadDelegationPreferences(app).Preferences
			roles.WorkflowSkillEnabled = true
			added := addAttnDelegationRoles(app, roles)

			var skills []string
			for _, root := range []string{".claude", ".agents"} {
				if entries, err := os.ReadDir(filepath.Join(toolHome, root, "skills")); err == nil {
					for _, entry := range entries {
						skills = append(skills, filepath.Join(root, "skills", entry.Name()))
					}
				}
			}
			if installed := len(skills) > 0; installed != tc.installs || added.Success != tc.installs {
				t.Errorf("%s installed skills %v and added the attn roles: %t (%s); want both %t",
					tc.name, skills, added.Success, protocol.Deref(added.Error), tc.installs)
			}
		})
	}
}
