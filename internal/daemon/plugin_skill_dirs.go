package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/pty"
)

type pluginSkillDir struct {
	HomeEnv     string `json:"home_env,omitempty"`
	DefaultHome string `json:"default_home"`
}

func (s pluginSkillDir) validate() error {
	if s.HomeEnv != "" && !validEnvironmentName(s.HomeEnv) {
		return fmt.Errorf("skill_dirs home_env %q must be a valid variable name", s.HomeEnv)
	}
	if s.DefaultHome == "" || !staysInside(s.DefaultHome) {
		return fmt.Errorf("skill_dirs default_home %q must be relative to the user's home", s.DefaultHome)
	}
	return nil
}

func (s pluginSkillDir) resolve(env []string, homeDir string) string {
	home := filepath.Join(homeDir, s.DefaultHome)
	if s.HomeEnv != "" {
		if value, _ := pty.LookupEnv(env, s.HomeEnv); strings.TrimSpace(value) != "" {
			home = strings.TrimSpace(value)
		}
	}
	return filepath.Join(home, "skills")
}

func (d *Daemon) workflowSkillsDirs(harnesses []string) func(homeDir string) []string {
	return func(homeDir string) []string {
		env := pty.MergeEnvironment(os.Environ(), d.latestLoginEnv())
		var dirs []string
		for _, harness := range harnesses {
			if dir := agentdriver.HarnessSkillsDir(harness, homeDir); dir != "" {
				dirs = append(dirs, dir)
				continue
			}
			driver, _ := d.ensurePluginRegistry().driver(harness)
			for _, dir := range driver.SkillDirs {
				dirs = append(dirs, dir.resolve(env, homeDir))
			}
		}
		return dirs
	}
}
