package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

const codexTrusted = "trusted"

type codexFolderTrust struct {
	path       string
	repository string
	saved      string
	worked     bool
}

func (d *Daemon) readCodexFolderTrust(path, profile string) (codexFolderTrust, error) {
	config, err := readCodexTrust(codexConfigPath())
	if err != nil {
		return codexFolderTrust{}, err
	}
	return assessCodexFolderTrust(path, config, d.store.StartedConversationDirectories(profile)), nil
}

func assessCodexFolderTrust(path string, config *codexTrustConfig, workedDirectories []string) codexFolderTrust {
	trust := codexFolderTrust{path: path, repository: path}
	worktrees := repositoryWorktrees(path)
	if len(worktrees) > 0 {
		trust.repository = worktrees[0]
	}
	if trust.saved = config.level(path); trust.saved == "" {
		trust.saved = config.level(trust.repository)
	}
	trust.worked = slices.ContainsFunc(append([]string{path}, worktrees...), func(directory string) bool {
		return slices.Contains(workedDirectories, directory)
	})
	return trust
}

func repositoryWorktrees(path string) []string {
	if top := repositoryTop(path); top != "" {
		return git.RepositoryWorktrees(top)
	}
	return nil
}

func workedInRepository(repository string, workedDirectories []string, originOf func(repository string) string) bool {
	origin, originRead := "", false
	seen := map[string]bool{}
	for _, directory := range workedDirectories {
		worktrees := repositoryWorktrees(directory)
		if len(worktrees) == 0 || seen[worktrees[0]] {
			continue
		}
		seen[worktrees[0]] = true
		if worktrees[0] == repository {
			return true
		}
		if !originRead {
			origin, originRead = originOf(repository), true
		}
		if origin != "" && originOf(worktrees[0]) == origin {
			return true
		}
	}
	return false
}

func (d *Daemon) repositoryOrigin(task gitTask, repository string) string {
	origin, err := gitValue(context.Background(), d.gitExecution(), task, func(ctx context.Context, client *git.Client) (string, error) {
		return client.RepositoryOrigin(ctx, repository), nil
	})
	if err != nil {
		return ""
	}
	return origin
}

func (d *Daemon) automationRepositoryOrigin(repository string) string {
	return d.repositoryOrigin(gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, repository)
}

func repositoryTop(path string) string {
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func (t codexFolderTrust) known() bool {
	return t.saved == codexTrusted || t.saved == "" && t.worked
}

func (d *Daemon) refuseUnknownCodexFolder(opts *ptybackend.SpawnOptions, profile string) error {
	if opts.Agent != string(protocol.SessionAgentCodex) || opts.UnattendedLaunch.IsZero() {
		return nil
	}
	d.codexTrustMu.Lock()
	defer d.codexTrustMu.Unlock()
	config, err := readCodexTrust(codexConfigPath())
	if err != nil {
		return err
	}
	return checkUnattendedCodexFolder(opts.CWD, config, d.store.StartedConversationDirectories(profile), d.automationRepositoryOrigin)
}

func checkUnattendedCodexFolder(path string, config *codexTrustConfig, workedDirectories []string, originOf func(repository string) string) error {
	trust := assessCodexFolderTrust(path, config, workedDirectories)
	switch {
	case trust.saved == codexTrusted:
		return nil
	case trust.saved != "":
		return fmt.Errorf("the Codex config saves %s as %s, so an automation does not run Codex there; trust the folder in Codex to let it run", trust.path, trust.saved)
	case !trust.worked && !workedInRepository(trust.repository, workedDirectories, originOf):
		return &fixableAutomationDeliveryError{
			message: fmt.Sprintf("no agent has worked in %s, its repository or another checkout of that repository in attn, and Codex does not trust it, so an automation will not trust it for you; open Codex there once and choose to trust it, or work there with any agent first", trust.path),
			fix:     store.AutomationRunFix{Agent: string(protocol.SessionAgentCodex), Directory: trust.repository},
		}
	}
	return nil
}

func codexConfigPath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "config.toml")
	}
	home, err := toolhome.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

type codexTrustConfig struct {
	path string
	raw  []byte
	read struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
}

func readCodexTrust(path string) (*codexTrustConfig, error) {
	if path == "" {
		return nil, errors.New("cannot find Codex's config to check folder trust: no CODEX_HOME and no home directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read Codex's config to check folder trust: %w", err)
	}
	config, err := parseCodexTrust(path, raw)
	if err != nil {
		return nil, fmt.Errorf("parse Codex's config %s to check folder trust: %w", path, err)
	}
	return config, nil
}

func (c *codexTrustConfig) level(folder string) string {
	if project, ok := c.read.Projects[folder]; ok {
		return project.TrustLevel
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		return c.read.Projects[resolved].TrustLevel
	}
	return ""
}

func (c *codexTrustConfig) addTrusted(folder string) error {
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	var entry bytes.Buffer
	if len(c.raw) > 0 {
		if !bytes.HasSuffix(c.raw, []byte("\n")) {
			entry.WriteString("\n")
		}
		entry.WriteString("\n")
	}
	fmt.Fprintf(&entry, "[projects.%s]\ntrust_level = %q\n", tomlBasicString(folder), codexTrusted)
	appended, err := parseCodexTrust(c.path, append(bytes.Clone(c.raw), entry.Bytes()...))
	if err != nil || appended.level(folder) != codexTrusted {
		return fmt.Errorf("cannot add trust for %s to Codex's config %s: %v", folder, c.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("add trust for %s to Codex's config: %w", folder, err)
	}
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("add trust for %s to Codex's config: %w", folder, err)
	}
	if _, err := f.Write(entry.Bytes()); err != nil {
		_ = f.Close()
		return fmt.Errorf("add trust for %s to Codex's config: %w", folder, err)
	}
	return f.Close()
}

func parseCodexTrust(path string, raw []byte) (*codexTrustConfig, error) {
	config := &codexTrustConfig{path: path, raw: raw}
	if _, err := toml.Decode(string(raw), &config.read); err != nil {
		return nil, err
	}
	return config, nil
}

func tomlBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
