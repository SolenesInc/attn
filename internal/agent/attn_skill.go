package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/toolhome"
)

// content/skills/attn/references/showing.md retains its HumanLayer MIT attribution.
var attnSkillFiles = prompts.AttnSkillFiles()
var attnWorkflowSkillFiles = prompts.AttnWorkflowSkillFiles()

const harnessSkillSyncEnv = "ATTN_HARNESS_SKILL_SYNC"

func installBundledSkill(files fs.ReadFileFS, root, skillDir string) error {
	expected := map[string]bool{}
	err := fs.WalkDir(files, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, root)
		relative = strings.TrimPrefix(relative, "/")
		target := filepath.Join(skillDir, filepath.FromSlash(relative))
		expected[target] = true
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create attn skill directory %s: %w", target, err)
			}
			return nil
		}

		content, err := fs.ReadFile(files, path)
		if err != nil {
			return fmt.Errorf("read bundled attn skill file %s: %w", path, err)
		}
		if current, err := os.ReadFile(target); err == nil && string(current) == string(content) {
			return nil
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read installed attn skill file %s: %w", target, err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("write attn skill file %s: %w", target, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return pruneOrphanedSkillFiles(skillDir, expected)
}

func installAttnSkill(skillDir string) error {
	return installBundledSkill(attnSkillFiles, "attn_skill", skillDir)
}

func installAttnWorkflowSkill(skillDir string) error {
	return installBundledSkill(attnWorkflowSkillFiles, "attn_workflow_skill", skillDir)
}

// Without this an installed skill accumulates stale content forever: a retired reference stays loadable by name and can contradict the current skill's guidance.
func pruneOrphanedSkillFiles(skillDir string, expected map[string]bool) error {
	return filepath.WalkDir(skillDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if expected[path] {
			return nil
		}
		if entry.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove orphaned attn skill directory %s: %w", path, err)
			}
			return fs.SkipDir
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove orphaned attn skill file %s: %w", path, err)
		}
		return nil
	})
}

func SkillFile(relative string) ([]byte, error) {
	return fs.ReadFile(attnSkillFiles, path.Join("attn_skill", relative))
}

func SkillReferenceNames() []string {
	entries, err := fs.ReadDir(attnSkillFiles, "attn_skill/references")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
	}
	return names
}

func ensureAttnClaudeSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Claude skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".claude", "skills", "attn"))
}

// ~/.agents/skills is not codex's alone: pi scans it unconditionally, so a delegated conversation agent does not depend on codex being configured.
func ensureAttnAgentsSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for agent skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".agents", "skills", "attn"))
}

func ensureAttnCopilotSkillInstalled() error {
	homeDir, err := toolhome.Dir()
	if err != nil {
		return fmt.Errorf("resolve home directory for Copilot skills: %w", err)
	}
	return installAttnSkill(filepath.Join(homeDir, ".copilot", "skills", "attn"))
}

func userGlobalSkillSyncEnabled() bool {
	profile := config.Profile()
	if profile == "" || profile == "dev" {
		return true
	}
	return os.Getenv("ATTN_AUTOMATION") == "1" && os.Getenv(harnessSkillSyncEnv) == "1" && strings.TrimSpace(os.Getenv(toolhome.EnvVar)) != ""
}

func EnsureClaudeSkillInstalled() (bool, error) {
	if !userGlobalSkillSyncEnabled() {
		return false, nil
	}
	return true, ensureAttnClaudeSkillInstalled()
}

func EnsureAgentsSkillInstalled() (bool, error) {
	if !userGlobalSkillSyncEnabled() {
		return false, nil
	}
	return true, ensureAttnAgentsSkillInstalled()
}

func EnsureCopilotSkillInstalled() (bool, error) {
	if !userGlobalSkillSyncEnabled() {
		return false, nil
	}
	return true, ensureAttnCopilotSkillInstalled()
}

func EnsureWorkflowSkillsInstalled(harnesses []string) ([]string, bool, error) {
	if !userGlobalSkillSyncEnabled() {
		return nil, false, nil
	}
	homeDir, err := toolhome.Dir()
	if err != nil {
		return nil, true, fmt.Errorf("resolve home directory for workflow skills: %w", err)
	}
	wanted := map[string]bool{}
	for _, harness := range harnesses {
		switch strings.TrimSpace(harness) {
		case "claude":
			wanted[filepath.Join(homeDir, ".claude", "skills", "attn-workflow")] = true
		case "codex", "pi":
			wanted[filepath.Join(homeDir, ".agents", "skills", "attn-workflow")] = true
		case "copilot":
			wanted[filepath.Join(homeDir, ".copilot", "skills", "attn-workflow")] = true
		}
	}
	paths := make([]string, 0, len(wanted))
	for target := range wanted {
		paths = append(paths, target)
	}
	slices.Sort(paths)
	for _, target := range paths {
		if err := installAttnWorkflowSkill(target); err != nil {
			return paths, true, fmt.Errorf("install attn-workflow at %s: %w", target, err)
		}
	}
	return paths, true, nil
}
