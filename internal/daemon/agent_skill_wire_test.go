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

func plantInstalledSkill(t *testing.T, skill, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(skill, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: " + name + "\ndescription: planted by this test\n---\n"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "references", "delegation.md"), []byte("old guidance"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStartingAttnRemovesTheSkillAnEarlierVersionInstalled(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	w := &world{World: prepareWorld(t, fakeagent.Claude, fakeagent.Codex)}
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	for h := range attnSkillRoots {
		plantInstalledSkill(t, installedAttnSkill(toolHome, h), "attn")
	}
	w.start()
	app := w.App()

	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		session := w.Spawn(app, h, w.Path(string(h)))
		w.Launched(session)
		if _, err := os.Stat(installedAttnSkill(toolHome, h)); !os.IsNotExist(err) {
			t.Errorf("%s still has the attn skill an earlier version installed (%v)", h, err)
		}
	}
}

func TestStartingAttnLeavesASkillTheUserNamedAttnFolderAlone(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	w := &world{World: prepareWorld(t, fakeagent.Claude, fakeagent.Codex)}
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	for h := range attnSkillRoots {
		plantInstalledSkill(t, installedAttnSkill(toolHome, h), "my-attention-notes")
	}
	w.start()
	app := w.App()

	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		w.Launched(w.Spawn(app, h, w.Path(string(h))))
		if _, err := os.Stat(filepath.Join(installedAttnSkill(toolHome, h), "SKILL.md")); err != nil {
			t.Errorf("attn removed the user's %s skill: %v", h, err)
		}
	}
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
