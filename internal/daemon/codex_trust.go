package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/toolhome"
)

const codexTrusted = "trusted"

type codexFolderTrust struct {
	path       string
	repository string
	saved      string
	worked     bool
	config     *codexTrustConfig
}

func (d *Daemon) readCodexFolderTrust(path string) (codexFolderTrust, error) {
	trust := codexFolderTrust{path: path, repository: path}
	var worktrees []string
	if top := repositoryTop(path); top != "" {
		worktrees = git.RepositoryWorktrees(top)
	}
	if len(worktrees) > 0 {
		trust.repository = worktrees[0]
	}
	config, err := readCodexTrust(codexConfigPath())
	if err != nil {
		return trust, err
	}
	trust.config = config
	if trust.saved = config.level(path); trust.saved == "" {
		trust.saved = config.level(trust.repository)
	}
	trust.worked = d.store.HasStartedConversationIn(append([]string{path}, worktrees...))
	return trust, nil
}

func (d *Daemon) workedInAnotherCheckout(repository string) bool {
	identity := d.originIdentity(repository)
	if identity == "" {
		return false
	}
	seen := map[string]bool{repository: true}
	for _, directory := range d.store.StartedConversationDirectories() {
		top := repositoryTop(directory)
		if top == "" {
			continue
		}
		if worktrees := git.RepositoryWorktrees(top); len(worktrees) > 0 {
			top = worktrees[0]
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		if d.originIdentity(top) == identity {
			return true
		}
	}
	return false
}

func (d *Daemon) originIdentity(repository string) string {
	identity, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(ctx context.Context, client *git.Client) (string, error) {
		host, ownerRepo, err := client.OriginHostOwnerRepo(ctx, repository)
		return strings.ToLower(host + "/" + ownerRepo), err
	})
	if err != nil || strings.HasSuffix(identity, "/") {
		return ""
	}
	return identity
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

func (d *Daemon) trustUnattendedCodexFolder(opts *ptybackend.SpawnOptions, shared bool) error {
	if opts.Agent != string(protocol.SessionAgentCodex) || opts.UnattendedLaunch.IsZero() {
		return nil
	}
	d.codexTrustMu.Lock()
	defer d.codexTrustMu.Unlock()
	trust, err := d.readCodexFolderTrust(opts.CWD)
	switch {
	case err != nil:
		return err
	case trust.saved == codexTrusted:
		return nil
	case trust.saved != "":
		return fmt.Errorf("the Codex config saves %s as %s, so an automation does not run Codex there; trust the folder in Codex to let it run", trust.path, trust.saved)
	case !trust.worked && !d.workedInAnotherCheckout(trust.repository):
		return fmt.Errorf("no agent has worked in %s, its repository or another checkout of that repository in attn, and Codex does not trust it, so an automation will not trust it for you; open Codex there once and choose to trust it, or work there with any agent first", trust.path)
	case shared:
		return nil
	}
	if err := trust.config.addTrusted(trust.repository); err != nil {
		return err
	}
	d.logf("automation launch: saved %s as trusted in Codex's config %s, since an agent session worked in it", trust.repository, trust.config.path)
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
