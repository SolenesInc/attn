package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/toolhome"
)

// attn_skill/references/showing.md adapts the show-me skill from
// github.com/humanlayer/skills (MIT, Copyright (c) 2026 HumanLayer).
//
//go:embed attn_skill
var attnSkillFiles embed.FS

func installAttnSkill(skillDir string) error {
	expected := map[string]bool{}
	err := fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(path, "attn_skill")
		relative = strings.TrimPrefix(relative, "/")
		target := filepath.Join(skillDir, filepath.FromSlash(relative))
		expected[target] = true
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create attn skill directory %s: %w", target, err)
			}
			return nil
		}

		content, err := attnSkillFiles.ReadFile(path)
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
	return attnSkillFiles.ReadFile(path.Join("attn_skill", relative))
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
	return profile == "" || profile == "dev"
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
