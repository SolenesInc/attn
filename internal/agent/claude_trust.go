package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/toolhome"
	"golang.org/x/text/unicode/norm"
)

func TrustClaudeWorkingDirectory(directory, root string) error {
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	directory = norm.NFC.String(directory)
	if root == "" {
		root, err = toolhome.Dir()
		if err != nil {
			return err
		}
	}
	path := filepath.Join(root, ".claude.json")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		return fmt.Errorf("trust Claude directory %s: acquire %s: %w; try again", directory, lock, err)
	}
	defer os.Remove(lock)
	config := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return fmt.Errorf("read Claude config %s: %w", path, err)
		}
		if config == nil {
			return fmt.Errorf("read Claude config %s: expected an object", path)
		}
	}
	projects := map[string]json.RawMessage{}
	if raw := config["projects"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return fmt.Errorf("read Claude projects in %s: %w", path, err)
		}
	}
	if projects == nil {
		projects = map[string]json.RawMessage{}
	}
	project := map[string]json.RawMessage{}
	if raw := projects[directory]; len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &project); err != nil {
			return fmt.Errorf("read Claude project %s in %s: %w", directory, path, err)
		}
	}
	if string(project["hasTrustDialogAccepted"]) == "true" {
		return nil
	}
	project["hasTrustDialogAccepted"] = json.RawMessage("true")
	projects[directory], err = json.Marshal(project)
	if err != nil {
		return err
	}
	config["projects"], err = json.Marshal(projects)
	if err != nil {
		return err
	}
	raw, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".claude-trust-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
