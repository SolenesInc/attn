package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

const claudeConfigLockWait = 2 * time.Second

func (d *Daemon) trustCrewHome(member crew.Member, agent, home string) {
	if strings.TrimSpace(member.CWD) != "" {
		return
	}
	var err error
	switch agent {
	case string(protocol.SessionAgentClaude):
		err = trustClaudeFolder(claudeConfigPath(), home)
	case string(protocol.SessionAgentCodex):
		err = d.trustCodexFolder(home)
	}
	if err != nil {
		d.logf("crew: %s's harness will ask whether to trust its home %s: %v", member.Name, home, err)
	}
}

func (d *Daemon) trustCodexFolder(folder string) error {
	d.codexTrustMu.Lock()
	defer d.codexTrustMu.Unlock()
	config, err := readCodexTrust(codexConfigPath())
	if err != nil {
		return err
	}
	if config.level(folder) != "" {
		return nil
	}
	return config.addTrusted(folder)
}

func claudeConfigPath() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home, err := toolhome.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

func trustClaudeFolder(path, folder string) error {
	if path == "" {
		return errors.New("cannot find Claude Code's config: no CLAUDE_CONFIG_DIR and no home directory")
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	unlock, err := lockClaudeConfig(path)
	if err != nil {
		return err
	}
	defer unlock()
	return addClaudeTrust(path, folder)
}

func lockClaudeConfig(path string) (unlock func(), err error) {
	lock := path + ".lock"
	deadline := time.Now().Add(claudeConfigLockWait)
	for {
		err := os.Mkdir(lock, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock Claude Code's config: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s stayed held past the %s limit", lock, claudeConfigLockWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func addClaudeTrust(path, folder string) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("parse Claude Code's config %s: %w", target, err)
	}
	projects := map[string]map[string]json.RawMessage{}
	if stored, ok := config["projects"]; ok {
		if err := json.Unmarshal(stored, &projects); err != nil {
			return fmt.Errorf("parse the projects in Claude Code's config %s: %w", target, err)
		}
	}
	project := projects[folder]
	if string(project["hasTrustDialogAccepted"]) == "true" {
		return nil
	}
	if project == nil {
		project = map[string]json.RawMessage{}
	}
	project["hasTrustDialogAccepted"] = json.RawMessage("true")
	projects[folder] = project
	if config["projects"], err = json.Marshal(projects); err != nil {
		return err
	}
	updated, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".attn-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(updated, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), target)
}
