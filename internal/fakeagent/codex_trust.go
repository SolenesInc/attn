package fakeagent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

func readCodexConfig(codexHome string) (map[string]any, error) {
	config := map[string]any{}
	if _, err := toml.DecodeFile(filepath.Join(codexHome, "config.toml"), &config); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return config, nil
}

func codexFolderTrust(config map[string]any, dir string) string {
	projects, _ := config["projects"].(map[string]any)
	project, _ := projects[dir].(map[string]any)
	level, _ := project["trust_level"].(string)
	return level
}

func setCodexFolderTrust(codexHome, dir, level string) error {
	config, err := readCodexConfig(codexHome)
	if err != nil {
		return err
	}
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		config["projects"] = projects
	}
	projects[dir] = map[string]any{"trust_level": level}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(config)
}
