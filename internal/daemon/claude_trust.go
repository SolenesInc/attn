package daemon

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

type claudeProject struct {
	HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
}

type claudeCheckout struct {
	top        string
	repository string
}

func (d *Daemon) settleClaudeFolderTrust(opts *ptybackend.SpawnOptions, trust *RepositoryTrust) error {
	if opts.Agent != string(protocol.SessionAgentClaude) || trust == nil {
		return nil
	}
	unattended := !opts.UnattendedLaunch.IsZero()
	cwd := git.CanonicalizePath(opts.CWD)
	checkout := readClaudeCheckout(cwd)
	config := claudeConfigPath()
	projects, err := readClaudeProjects(config)
	if err != nil {
		if unattended {
			return fmt.Errorf("an automation cannot check whether Claude trusts %s: %w", opts.CWD, err)
		}
		d.logf("delegation launch: Claude may ask whether to trust %s: %v", opts.CWD, err)
		return nil
	}
	if claudeHasTrust(projects, cwd, checkout) {
		return nil
	}
	origin := ""
	if checkout.repository != "" {
		origin = d.repositoryOrigin(gitTask{Kind: gitTaskSessionIdentity, Lane: gitInteractive}, checkout.repository)
	}
	if origin == "" || !slices.Contains(trust.KnownOrigins, origin) {
		if unattended {
			return &fixableAutomationDeliveryError{
				message: fmt.Sprintf("no agent has started a conversation in the repository of %s in this profile, on any machine, and Claude does not trust it, so an automation will not trust it for you; open Claude there once and choose to trust it", opts.CWD),
				fix:     store.AutomationRunFix{Agent: string(protocol.SessionAgentClaude), Directory: cmp.Or(checkout.repository, cwd)},
			}
		}
		return nil
	}
	if err := trustClaudeFolder(config, checkout.repository); err != nil {
		if unattended {
			return fmt.Errorf("record Claude's trust for %s: %w", checkout.repository, err)
		}
		d.logf("delegation launch: Claude may ask whether to trust %s: %v", checkout.repository, err)
		return nil
	}
	d.logf("agent launch: saved %s as trusted in Claude's config %s, since an agent session started a conversation in %s", checkout.repository, config, origin)
	return nil
}

func readClaudeCheckout(cwd string) claudeCheckout {
	top := repositoryTop(cwd)
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
