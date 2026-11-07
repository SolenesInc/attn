package agent

import (
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/toolhome"
)

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

func installAttnWorkflowSkill(skillDir string) error {
	return installBundledSkill(attnWorkflowSkillFiles, "attn_workflow_skill", skillDir)
}

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

func userGlobalSkillSyncEnabled() bool {
	instance := config.Instance()
	if instance == "" || instance == "dev" {
		return true
	}
	return os.Getenv("ATTN_AUTOMATION") == "1" && os.Getenv(harnessSkillSyncEnv) == "1" && strings.TrimSpace(os.Getenv(toolhome.EnvVar)) != ""
}

func RemoveRetiredAttnSkill() (bool, []string, error) {
	if !userGlobalSkillSyncEnabled() {
		return false, nil, nil
	}
	homeDir, err := toolhome.Dir()
	if err != nil {
		return true, nil, fmt.Errorf("resolve home directory for the retired attn skill: %w", err)
	}
	var kept []string
	for _, root := range []string{".claude", ".agents"} {
		skillDir := filepath.Join(homeDir, root, "skills", "attn")
		removed, err := removeSkillIfAttnInstalledAllOfIt(skillDir)
		if err != nil {
			return true, kept, err
		}
		if !removed {
			kept = append(kept, skillDir)
		}
	}
	return true, kept, nil
}

//go:embed retired_attn_skill_blobs.txt
var retiredAttnSkillBlobList string

var retiredAttnSkillBlobs = sync.OnceValue(func() map[string]bool {
	blobs := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(retiredAttnSkillBlobList), "\n") {
		blobs[line] = true
	}
	return blobs
})

func gitBlobID(content []byte) string {
	sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...))
	return hex.EncodeToString(sum[:])
}

func removeSkillIfAttnInstalledAllOfIt(skillDir string) (bool, error) {
	info, err := os.Lstat(skillDir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil || !info.IsDir() {
		return false, nil
	}
	attnInstalledAll := true
	err = filepath.WalkDir(skillDir, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			attnInstalledAll = false
			return fs.SkipAll
		}
		relative, err := filepath.Rel(skillDir, file)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read %s to check whether attn installed it: %w", file, err)
		}
		if !retiredAttnSkillBlobs()[gitBlobID(content)+" "+filepath.ToSlash(relative)] {
			attnInstalledAll = false
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("check whether attn installed the skill at %s: %w", skillDir, err)
	}
	if !attnInstalledAll {
		return false, nil
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return false, fmt.Errorf("remove the retired attn skill at %s: %w", skillDir, err)
	}
	return true, nil
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
		case "codex":
			wanted[filepath.Join(homeDir, ".agents", "skills", "attn-workflow")] = true
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
