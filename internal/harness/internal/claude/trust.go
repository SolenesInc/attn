package claude

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/toolhome"
)

const claudeConfigLockWait = 2 * time.Second

type claudeProject struct {
	HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
}

type claudeCheckout struct {
	top        string
	repository string
}

func (h claudeHarness) settleFolderTrust(req harness.LaunchRequest) error {
	trust := req.Trust
	if trust == nil {
		return nil
	}
	unattended := !req.Unattended.IsZero()
	cwd := git.CanonicalizePath(req.Dir)
	checkout := readClaudeCheckout(cwd)
	config := claudeConfigPath()
	projects, err := readClaudeProjects(config)
	if err != nil {
		if unattended {
			return fmt.Errorf("an automation cannot check whether Claude trusts %s: %w", req.Dir, err)
		}
		h.host.Logf("delegation launch: Claude may ask whether to trust %s: %v", req.Dir, err)
		return nil
	}
	if claudeHasTrust(projects, cwd, checkout) {
		return nil
	}
	origin := ""
	if checkout.repository != "" {
		origin = h.host.RepositoryOrigin(checkout.repository)
	}
	if origin == "" || !slices.Contains(trust.KnownOrigins, origin) {
		if unattended {
			return &harness.FixableError{
				Message: fmt.Sprintf("no agent has started a conversation in the repository of %s in this profile, on any machine, and Claude does not trust it, so an automation will not trust it for you; open Claude there once and choose to trust it", req.Dir),
				Fix:     harness.Fix{Harness: claudeID, Dir: cmp.Or(checkout.repository, cwd)},
			}
		}
		return nil
	}
	if err := trustClaudeFolder(config, checkout.repository); err != nil {
		if unattended {
			return fmt.Errorf("record Claude's trust for %s: %w", checkout.repository, err)
		}
		h.host.Logf("delegation launch: Claude may ask whether to trust %s: %v", checkout.repository, err)
		return nil
	}
	h.host.Logf("agent launch: saved %s as trusted in Claude's config %s, since an agent session started a conversation in %s", checkout.repository, config, origin)
	return nil
}

func readClaudeCheckout(cwd string) claudeCheckout {
	top := git.RepositoryTop(cwd)
	if top == "" {
		return claudeCheckout{}
	}
	if main := git.GetMainRepoFromWorktree(top); main != "" {
		return claudeCheckout{top: top, repository: git.CanonicalizePath(main)}
	}
	return claudeCheckout{top: top, repository: top}
}
func readClaudeProjects(path string) (map[string]claudeProject, error) {
	if path == "" {
		return nil, fmt.Errorf("cannot find Claude Code's config: no CLAUDE_CONFIG_DIR and no home directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Claude Code's config: %w", err)
	}
	var config struct {
		Projects map[string]claudeProject `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parse Claude Code's config %s: %w", path, err)
	}
	return config.Projects, nil
}

func claudeHasTrust(projects map[string]claudeProject, cwd string, checkout claudeCheckout) bool {
	if checkout.repository != "" && projects[checkout.repository].HasTrustDialogAccepted {
		return true
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if projects[dir].HasTrustDialogAccepted {
			return true
		}
		if dir == checkout.top || filepath.Dir(dir) == dir {
			return false
		}
	}
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
