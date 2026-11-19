package daemon

import (
	"errors"
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
	if s.HomeEnv != "" && !agentdriver.ValidEnvironmentName(s.HomeEnv) {
		return fmt.Errorf("skill_dirs home_env %q must be a valid variable name", s.HomeEnv)
	}
	if s.DefaultHome == "" || !filepath.IsAbs(s.DefaultHome) && !agentdriver.StaysInside(s.DefaultHome) {
		return fmt.Errorf("skill_dirs default_home %q must be absolute or relative to the user's home", s.DefaultHome)
	}
	return nil
}

func (s pluginSkillDir) resolve(env []string, homeDir string) string {
	home := s.DefaultHome
	if !filepath.IsAbs(home) {
		home = filepath.Join(homeDir, home)
	}
	if s.HomeEnv != "" {
		if value, _ := pty.LookupEnv(env, s.HomeEnv); strings.TrimSpace(value) != "" {
			home = strings.TrimSpace(value)
		}
	}
	return filepath.Join(home, "skills")
}

func (d *Daemon) workflowSkillsDirs(harnesses []string) func(homeDir string) ([]string, error) {
	return func(homeDir string) ([]string, error) {
		env := pty.MergeEnvironment(os.Environ(), d.latestLoginEnv())
		lookup := func(key string) string {
			value, _ := pty.LookupEnv(env, key)
			return value
		}
		var dirs []string
		var refusals []error
		for _, harness := range harnesses {
			if dir := agentdriver.HarnessSkillsDir(harness, homeDir); dir != "" {
				dirs = append(dirs, dir)
				continue
			}
			if resolver, ok := agentdriver.Get(harness).(agentdriver.SkillDirsResolver); ok {
				resolved, err := resolver.SkillDirs(lookup, homeDir)
				if err != nil {
					refusals = append(refusals, err)
				}
				dirs = append(dirs, resolved...)
				continue
			}
			driver, _ := d.ensurePluginRegistry().driver(harness)
			for _, dir := range driver.SkillDirs {
				dirs = append(dirs, dir.resolve(env, homeDir))
			}
		}
		return dirs, errors.Join(refusals...)
	}
}
