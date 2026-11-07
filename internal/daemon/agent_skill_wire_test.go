package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
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

func checkSkillUnchanged(t *testing.T, skillDir string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		if got, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(name))); err != nil || string(got) != contents {
			t.Errorf("attn removed or changed %s in the user's skill at %s: %q, %v", name, skillDir, got, err)
		}
	}
}

func TestStartingAttnRemovesTheSkillAnEarlierVersionInstalled(t *testing.T) {
	installed := skillAnEarlierVersionInstalled(t)
	startWithCodexUnavailable(t, func(toolHome string) {
		for h := range attnSkillRoots {
			plantFiles(t, installedAttnSkill(toolHome, h), installed)
		}
	}, func(t *testing.T, toolHome string) {
		for h := range attnSkillRoots {
			if _, err := os.Stat(installedAttnSkill(toolHome, h)); !os.IsNotExist(err) {
				t.Errorf("%s still has the attn skill an earlier version installed (%v)", h, err)
			}
		}
	})
}

func TestStartingAttnKeepsAUserChangedCopyOfTheOldSkillWhole(t *testing.T) {
	installed := skillAnEarlierVersionInstalled(t)
	customized := map[string]string{
		"SKILL.md":                 strings.Replace(installed["SKILL.md"], "name: attn\n", "name: \"attn\"\n", 1),
		"references/delegation.md": installed["references/delegation.md"],
	}
	withNote := map[string]string{
		"SKILL.md":                 installed["SKILL.md"],
		"references/delegation.md": installed["references/delegation.md"],
		"references/my-notes.md":   "Notes I keep next to the attn skill.\n",
	}
	if customized["SKILL.md"] == installed["SKILL.md"] {
		t.Fatal("the old SKILL.md no longer says name: attn; pick another edit")
	}
	startWithCodexUnavailable(t, func(toolHome string) {
		plantFiles(t, installedAttnSkill(toolHome, fakeagent.Claude), customized)
		plantFiles(t, installedAttnSkill(toolHome, fakeagent.Codex), withNote)
	}, func(t *testing.T, toolHome string) {
		checkSkillUnchanged(t, installedAttnSkill(toolHome, fakeagent.Claude), customized)
		checkSkillUnchanged(t, installedAttnSkill(toolHome, fakeagent.Codex), withNote)
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
			checkSkillUnchanged(t, installedAttnSkill(toolHome, h), userSkill)
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
