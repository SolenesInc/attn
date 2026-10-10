package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/toolhome"
	"golang.org/x/text/unicode/norm"
)

const claudeConfigLockLease = 10 * time.Second

type claudeConfigLock struct {
	path string
	info os.FileInfo
	stop chan struct{}
	done chan struct{}
}

func acquireClaudeConfigLock(path string) (*claudeConfigLock, error) {
	for {
		if err := os.Mkdir(path, 0o700); err == nil {
			break
		} else if !os.IsExist(err) {
			return nil, err
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if time.Since(info.ModTime()) <= claudeConfigLockLease {
			return nil, fmt.Errorf("Claude config lock %s is in use; try again", path)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	lock := &claudeConfigLock{path: path, info: info, stop: make(chan struct{}), done: make(chan struct{})}
	go lock.renew()
	return lock, nil
}

func (l *claudeConfigLock) owned() bool {
	info, err := os.Stat(l.path)
	return err == nil && os.SameFile(l.info, info)
}

func (l *claudeConfigLock) renew() {
	defer close(l.done)
	ticker := time.NewTicker(claudeConfigLockLease / 2)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			now := time.Now()
			if !l.owned() || os.Chtimes(l.path, now, now) != nil {
				return
			}
		}
	}
}

func (l *claudeConfigLock) release() {
	close(l.stop)
	<-l.done
	if l.owned() {
		_ = os.Remove(l.path)
	}
}

func TrustClaudeWorkingDirectory(directory, root string) error {
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	directory = norm.NFC.String(directory)
	legacyRoot := root
	if root == "" {
		root, err = toolhome.Dir()
		if err != nil {
			return err
		}
		legacyRoot = filepath.Join(root, ".claude")
	}
	path := filepath.Join(root, ".claude.json")
	legacy := filepath.Join(legacyRoot, ".config.json")
	if _, err := os.Stat(legacy); err == nil {
		path = legacy
		root = legacyRoot
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	lock, err := acquireClaudeConfigLock(path + ".lock")
	if err != nil {
		return fmt.Errorf("trust Claude directory %s: %w", directory, err)
	}
	defer lock.release()
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
	if !lock.owned() {
		return fmt.Errorf("Claude config lock %s was lost; try again", lock.path)
	}
	return os.Rename(file.Name(), path)
}
