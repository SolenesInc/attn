package agent

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

func TestAttnSkillUsesTheSeedBodyAndANeutralHarvestExample(t *testing.T) {
	contents, err := attnSkillFiles.ReadFile("attn_skill/references/delegated-agent.md")
	if err != nil {
		t.Fatalf("read delegated-agent reference: %v", err)
	}
	for _, want := range []string{
		"Harvest only when the assigned outcome and required verification are complete",
		`attn seed harvest <seed-id> -m "<outcome and verification>"`,
	} {
		if !strings.Contains(string(contents), want) {
			t.Fatalf("delegated-agent reference dropped %q:\n%s", want, contents)
		}
	}
}

func TestEnsureWorkflowSkillsInstalledUsesSupportedIsolatedRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "dev")

	paths, attempted, err := EnsureWorkflowSkillsInstalled([]string{"codex", "pi", "claude", "copilot", "plugin-only"})
	if err != nil || !attempted {
		t.Fatalf("paths=%v attempted=%v error=%v", paths, attempted, err)
	}
	want := []string{
		filepath.Join(home, ".agents", "skills", "attn-workflow"),
		filepath.Join(home, ".claude", "skills", "attn-workflow"),
		filepath.Join(home, ".copilot", "skills", "attn-workflow"),
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths=%v, want %v", paths, want)
	}
	for _, skillDir := range paths {
		for _, relative := range []string{"SKILL.md", filepath.Join("references", "planning.md")} {
			if _, err := os.Stat(filepath.Join(skillDir, relative)); err != nil {
				t.Fatalf("installed %s: %v", filepath.Join(skillDir, relative), err)
			}
		}
	}

	again, attempted, err := EnsureWorkflowSkillsInstalled([]string{"pi", "codex"})
	if err != nil || !attempted || len(again) != 1 || again[0] != want[0] {
		t.Fatalf("repeat paths=%v attempted=%v error=%v", again, attempted, err)
	}
}

func TestEnsureWorkflowSkillsInstalledSkipsVerificationProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "fixture-lab")
	paths, attempted, err := EnsureWorkflowSkillsInstalled([]string{"codex"})
	if err != nil || attempted || len(paths) != 0 {
		t.Fatalf("paths=%v attempted=%v error=%v", paths, attempted, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("verification profile wrote user-global skills: %v", err)
	}
}

func assertAttnSkillTree(t *testing.T, skillDir string) {
	t.Helper()

	expected := map[string]bool{}
	err := fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "attn_skill" {
			return nil
		}

		relative, err := filepath.Rel("attn_skill", path)
		if err != nil {
			return err
		}
		relative = filepath.FromSlash(relative)
		expected[relative] = true

		installedPath := filepath.Join(skillDir, relative)
		if entry.IsDir() {
			info, err := os.Stat(installedPath)
			if err != nil {
				t.Fatalf("stat installed skill directory %s: %v", installedPath, err)
			}
			if !info.IsDir() {
				t.Fatalf("installed skill path %s is not a directory", installedPath)
			}
			return nil
		}

		want, err := attnSkillFiles.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatalf("read installed skill file %s: %v", installedPath, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("installed skill file %s differs from bundled source", installedPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundled attn skill: %v", err)
	}

	err = filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == skillDir {
			return nil
		}
		relative, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		if !expected[relative] {
			t.Fatalf("installed skill contains unexpected path %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk installed attn skill: %v", err)
	}
}

func TestEnsureAttnClaudeSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnClaudeSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".claude", "skills", "attn"))
}

func TestEnsureAttnClaudeSkillInstalledPrunesOrphanedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	skillDir := filepath.Join(home, ".claude", "skills", "attn")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	orphanFile := filepath.Join(skillDir, "references", "chief-of-staff.md")
	if err := os.WriteFile(orphanFile, []byte("stale, retired guidance"), 0o644); err != nil {
		t.Fatalf("seed orphaned reference: %v", err)
	}
	orphanDir := filepath.Join(skillDir, "references", "retired-subdir")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("seed orphaned directory: %v", err)
	}

	if err := ensureAttnClaudeSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnClaudeSkillInstalled() error = %v", err)
	}

	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference file was not pruned: stat err = %v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference directory was not pruned: stat err = %v", err)
	}
	assertAttnSkillTree(t, skillDir)
}

func TestEnsureAttnAgentsSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnAgentsSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnAgentsSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".agents", "skills", "attn"))
}

func TestEnsureAttnCopilotSkillInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	if err := ensureAttnCopilotSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnCopilotSkillInstalled() error = %v", err)
	}

	assertAttnSkillTree(t, filepath.Join(home, ".copilot", "skills", "attn"))
}

func TestEnsureAttnCopilotSkillInstalledPrunesOrphanedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)

	skillDir := filepath.Join(home, ".copilot", "skills", "attn")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	orphanFile := filepath.Join(skillDir, "references", "chief-of-staff.md")
	if err := os.WriteFile(orphanFile, []byte("stale, retired guidance"), 0o644); err != nil {
		t.Fatalf("seed orphaned reference: %v", err)
	}

	if err := ensureAttnCopilotSkillInstalled(); err != nil {
		t.Fatalf("ensureAttnCopilotSkillInstalled() error = %v", err)
	}

	if _, err := os.Stat(orphanFile); !os.IsNotExist(err) {
		t.Fatalf("orphaned reference file was not pruned: stat err = %v", err)
	}
	assertAttnSkillTree(t, skillDir)
}

func TestUserGlobalSkillSyncIsSkippedOutsideDefaultAndDevProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "fixture-lab")

	for name, ensure := range map[string]func() (bool, error){
		"claude":  EnsureClaudeSkillInstalled,
		"agents":  EnsureAgentsSkillInstalled,
		"copilot": EnsureCopilotSkillInstalled,
	} {
		t.Run(name, func(t *testing.T) {
			synced, err := ensure()
			if err != nil {
				t.Fatalf("ensure skill: %v", err)
			}
			if synced {
				t.Fatal("a verification profile synchronized a user-global skill")
			}
		})
	}

	for _, root := range []string{".claude", ".agents", ".copilot"} {
		if _, err := os.Stat(filepath.Join(home, root)); !os.IsNotExist(err) {
			t.Fatalf("verification profile wrote %s: stat err = %v", root, err)
		}
	}
}

func TestUserGlobalSkillSyncRunsForExplicitIsolatedHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "fixture-lab")
	t.Setenv("ATTN_AUTOMATION", "1")
	t.Setenv(harnessSkillSyncEnv, "1")

	synced, err := EnsureAgentsSkillInstalled()
	if err != nil {
		t.Fatalf("ensure skill: %v", err)
	}
	if !synced {
		t.Fatal("explicit isolated harness skipped skill synchronization")
	}
	assertAttnSkillTree(t, filepath.Join(home, ".agents", "skills", "attn"))
}

func TestUserGlobalSkillSyncRunsForDevProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("ATTN_PROFILE", "dev")

	for name, test := range map[string]struct {
		ensure   func() (bool, error)
		skillDir string
	}{
		"claude":  {EnsureClaudeSkillInstalled, filepath.Join(home, ".claude", "skills", "attn")},
		"agents":  {EnsureAgentsSkillInstalled, filepath.Join(home, ".agents", "skills", "attn")},
		"copilot": {EnsureCopilotSkillInstalled, filepath.Join(home, ".copilot", "skills", "attn")},
	} {
		t.Run(name, func(t *testing.T) {
			synced, err := test.ensure()
			if err != nil {
				t.Fatalf("ensure skill: %v", err)
			}
			if !synced {
				t.Fatal("dev profile skipped user-global skill synchronization")
			}
			assertAttnSkillTree(t, test.skillDir)
		})
	}
}
