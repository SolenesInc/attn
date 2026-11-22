package sigma

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

func skillDirs(lookupEnv func(string) string, homeDir string) ([]string, error) {
	sigmaHome := envHome(lookupEnv("SIGMA_HOME"), filepath.Join(homeDir, ".sigma"))
	claudeHome := envHome(lookupEnv("CLAUDE_CONFIG_DIR"), filepath.Join(homeDir, ".claude"))
	providers, relative := claudeProviderHomes(sigmaHome, homeDir)
	if len(relative) > 0 {
		return nil, fmt.Errorf("the Claude providers %s in Sigma's config.toml name a relative config_dir or env.CLAUDE_CONFIG_DIR; make it absolute (config_dir may start with ~/) so attn can install the skills Attn roles need there", strings.Join(relative, ", "))
	}
	dirs := []string{filepath.Join(sigmaHome, "skills"), filepath.Join(claudeHome, "skills")}
	for _, home := range providers {
		dirs = append(dirs, filepath.Join(home, "skills"))
	}
	return dirs, nil
}

func envHome(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

type configProvider struct {
	Driver    string            `toml:"driver"`
	ConfigDir string            `toml:"config_dir"`
	Env       map[string]string `toml:"env"`
}

func claudeProviderHomes(sigmaHome, homeDir string) (homes, relative []string) {
	raw, err := os.ReadFile(filepath.Join(sigmaHome, "config.toml"))
	if err != nil {
		return nil, nil
	}
	var config struct {
		Providers map[string]configProvider `toml:"providers"`
	}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, nil
	}
	for id, provider := range config.Providers {
		if provider.Driver != "claude" {
			continue
		}
		dir := provider.Env["CLAUDE_CONFIG_DIR"]
		if provider.ConfigDir != "" {
			dir = expandUserHome(provider.ConfigDir, homeDir)
		}
		switch {
		case dir == "":
		case filepath.IsAbs(dir):
			if !slices.Contains(homes, dir) {
				homes = append(homes, dir)
			}
		default:
			relative = append(relative, id)
		}
	}
	slices.Sort(homes)
	slices.Sort(relative)
	return homes, relative
}

func expandUserHome(path, homeDir string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[1:])
	}
	return path
}
