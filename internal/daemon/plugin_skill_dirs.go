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

func (s pluginSkillDir) resolve(lookupEnv func(string) string, homeDir string) string {
	home := s.DefaultHome
	if !filepath.IsAbs(home) {
		home = filepath.Join(homeDir, home)
	}
	if s.HomeEnv != "" {
		if value := strings.TrimSpace(lookupEnv(s.HomeEnv)); value != "" {
			home = value
		}
	}
	return filepath.Join(home, "skills")
}

func (d *Daemon) workflowSkillsDirs(harnesses []string) func(homeDir string) ([]string, error) {
	return func(homeDir string) ([]string, error) {
		lookupEnv := d.loginEnvLookup()
		var dirs []string
		var refusals []error
		for _, id := range harnesses {
			skillDirs := d.factsOrNone(id).SkillDirs
			if skillDirs == nil {
				continue
			}
			resolved, err := skillDirs(lookupEnv, homeDir)
			if err != nil {
				refusals = append(refusals, err)
			}
			dirs = append(dirs, resolved...)
		}
		return dirs, errors.Join(refusals...)
	}
}

func (d *Daemon) pluginSkillsDirs(skillDirs []pluginSkillDir) func(homeDir string) ([]string, error) {
	return func(homeDir string) ([]string, error) {
		return resolvePluginSkillsDirs(skillDirs, d.loginEnvLookup(), homeDir), nil
	}
}

func (d *Daemon) loginEnvLookup() func(string) string {
	env := pty.MergeEnvironment(os.Environ(), d.latestLoginEnv())
	return func(key string) string {
		value, _ := pty.LookupEnv(env, key)
		return value
	}
}

func resolvePluginSkillsDirs(skillDirs []pluginSkillDir, lookupEnv func(string) string, homeDir string) []string {
	dirs := make([]string, 0, len(skillDirs))
	for _, dir := range skillDirs {
		dirs = append(dirs, dir.resolve(lookupEnv, homeDir))
	}
	return dirs
}
