package daemon_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

var attnSkillRoots = map[fakeagent.Harness]string{
	fakeagent.Claude:  ".claude",
	fakeagent.Codex:   ".agents",
	fakeagent.Copilot: ".copilot",
}

func installedAttnSkill(toolHome string, h fakeagent.Harness) string {
	return filepath.Join(toolHome, attnSkillRoots[h], "skills", "attn")
}

func plantRetiredSkillFiles(t *testing.T, skill string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(skill, "references", "retired-topic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "references", "workflow.md"), []byte("retired guidance"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertShippedAttnSkill(t *testing.T, h fakeagent.Harness, skill string) {
	t.Helper()
	shipped := prompts.AttnSkillFiles()
	want := map[string]bool{}
	err := fs.WalkDir(shipped, "attn_skill", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative := strings.TrimPrefix(path, "attn_skill/")
		want[relative] = true
		content, _ := fs.ReadFile(shipped, path)
		if installed, err := os.ReadFile(filepath.Join(skill, relative)); err != nil || !bytes.Equal(installed, content) {
			t.Errorf("%s's attn skill file %s is not the shipped one (%v)", h, relative, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(skill, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == skill {
			return err
		}
		relative, _ := filepath.Rel(skill, path)
		if entry.IsDir() && !shipsUnder(want, relative) {
			t.Errorf("%s's attn skill keeps the directory %s, which attn no longer ships", h, relative)
			return fs.SkipDir
		}
		if !entry.IsDir() && !want[relative] {
			t.Errorf("%s's attn skill keeps %s, which attn no longer ships", h, relative)
		}
		return nil
	})
}

func shipsUnder(files map[string]bool, dir string) bool {
	for file := range files {
		if strings.HasPrefix(file, dir+"/") {
			return true
		}
	}
	return false
}

func TestEveryHarnessAttnRunsFindsTheShippedAttnSkillAndNothingRetired(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot)
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	app := w.App()
	for h := range attnSkillRoots {
		plantRetiredSkillFiles(t, installedAttnSkill(toolHome, h))
	}

	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		w.Launched(w.Spawn(app, h, w.Path(string(h))))
		assertShippedAttnSkill(t, h, installedAttnSkill(toolHome, h))
	}
	assertShippedAttnSkill(t, fakeagent.Copilot, installedAttnSkill(toolHome, fakeagent.Copilot))
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
			for _, root := range []string{".claude", ".agents", ".copilot"} {
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
