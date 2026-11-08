package fakeagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func claudeConfigFile(toolHome string) string {
	return filepath.Join(toolHome, ".claude.json")
}

func readClaudeTrust(toolHome string) (map[string]map[string]any, error) {
	raw, err := os.ReadFile(claudeConfigFile(toolHome))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if config.Projects == nil {
		config.Projects = map[string]map[string]any{}
	}
	return config.Projects, nil
}

func claudeTrustsCurrentRepository(toolHome string) bool {
	projects, err := readClaudeTrust(toolHome)
	if err != nil {
		return false
	}
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return false
	}
	main := filepath.Dir(strings.TrimSpace(string(out)))
	return projects[main]["hasTrustDialogAccepted"] == true
}

func setClaudeTrust(toolHome, dir string) error {
	projects, err := readClaudeTrust(toolHome)
	if err != nil {
		return err
	}
	projects[dir] = map[string]any{"hasTrustDialogAccepted": true}
	raw, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(toolHome, 0o700); err != nil {
		return err
	}
	return os.WriteFile(claudeConfigFile(toolHome), raw, 0o600)
}
