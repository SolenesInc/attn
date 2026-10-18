package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	attngit "github.com/victorarias/attn/internal/git"
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

func codexUnderUntrusted(config map[string]any, dir string) bool {
	root, _ := attngit.RepositoryRoot(context.Background(), dir)
	for _, start := range []string{filepath.Dir(dir), root} {
		for d := start; d != ""; d = filepath.Dir(d) {
			if codexFolderTrust(config, d) == "untrusted" {
				return true
			}
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return false
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

func (s *codexAppServer) writeConfig(raw json.RawMessage) (any, func(), error) {
	var p struct {
		Edits []struct {
			KeyPath string `json:"keyPath"`
			Value   string `json:"value"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, err
	}
	for _, edit := range p.Edits {
		quoted, ok := strings.CutPrefix(edit.KeyPath, "projects.")
		if ok {
			quoted, ok = strings.CutSuffix(quoted, ".trust_level")
		}
		dir, err := strconv.Unquote(quoted)
		if !ok || err != nil {
			return nil, nil, fmt.Errorf("the fake app-server writes only folder trust, not %s", edit.KeyPath)
		}
		if err := setCodexFolderTrust(s.cfg.CodexHome, dir, edit.Value); err != nil {
			return nil, nil, err
		}
	}
	return map[string]any{"status": "ok"}, nil, nil
}
