package plugins

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	APIVersion   = 7
	ManifestName = "attn-plugin.toml"
)

type EntrypointKind string

const (
	EntrypointBun        EntrypointKind = "bun"
	EntrypointExecutable EntrypointKind = "executable"
)

type Manifest struct {
	Name           string `toml:"name" json:"name"`
	Version        string `toml:"version" json:"version"`
	AttnAPIVersion int    `toml:"attn_api_version" json:"attn_api_version"`
	Description    string `toml:"description" json:"description,omitempty"`
	Plugin         struct {
		Entrypoint string         `toml:"entrypoint" json:"entrypoint,omitempty"`
		Kind       EntrypointKind `toml:"kind" json:"kind"`
		Path       string         `toml:"path" json:"path"`
	} `toml:"plugin" json:"plugin"`

	Dir        string     `toml:"-" json:"dir"`
	LinkTarget string     `toml:"-" json:"link_target,omitempty"`
	Source     string     `toml:"-" json:"source,omitempty"`
	SourceKind SourceKind `toml:"-" json:"source_kind,omitempty"`
}

type ManifestIssue struct {
	Path string `json:"path"`
	Err  error  `json:"-"`
}

type InstallOptions struct {
	Env      []string
	CloneGit func(context.Context, string, string, []string) ([]byte, error)
}

func (i ManifestIssue) Error() string {
	return fmt.Sprintf("%s: %v", i.Path, i.Err)
}

func LoadManifest(path string) (Manifest, error) {
	var manifest Manifest
	if _, err := toml.DecodeFile(path, &manifest); err != nil {
		return Manifest{}, err
	}

	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Plugin.Entrypoint = strings.TrimSpace(manifest.Plugin.Entrypoint)
	manifest.Plugin.Kind = EntrypointKind(strings.TrimSpace(string(manifest.Plugin.Kind)))
	manifest.Plugin.Path = strings.TrimSpace(manifest.Plugin.Path)
	manifest.Dir = filepath.Dir(path)
	if manifest.Plugin.Entrypoint != "" {
		if manifest.Plugin.Kind != "" || manifest.Plugin.Path != "" {
			return Manifest{}, errors.New("plugin.entrypoint cannot be combined with plugin.kind or plugin.path")
		}
		manifest.Plugin.Kind = EntrypointBun
		manifest.Plugin.Path = manifest.Plugin.Entrypoint
	}

	switch {
	case manifest.Name == "":
		return Manifest{}, errors.New("name is required")
	case manifest.Version == "":
		return Manifest{}, errors.New("version is required")
	case manifest.AttnAPIVersion != APIVersion:
		return Manifest{}, fmt.Errorf("unsupported attn_api_version %d", manifest.AttnAPIVersion)
	case manifest.Plugin.Kind != EntrypointBun && manifest.Plugin.Kind != EntrypointExecutable:
		return Manifest{}, errors.New(`plugin.kind must be "bun" or "executable"`)
	case manifest.Plugin.Path == "":
		return Manifest{}, errors.New("plugin.path is required")
	case filepath.IsAbs(manifest.Plugin.Path):
		return Manifest{}, errors.New("plugin.path must be relative to the plugin directory")
	}

	entrypointPath := filepath.Clean(filepath.Join(manifest.Dir, manifest.Plugin.Path))
	if !pathWithinDir(manifest.Dir, entrypointPath) {
		return Manifest{}, errors.New("plugin.path must stay within the plugin directory")
	}
	info, err := os.Stat(entrypointPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin.path %q: %w", manifest.Plugin.Path, err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("plugin.path %q must be a regular file", manifest.Plugin.Path)
	}
	if manifest.Plugin.Kind == EntrypointExecutable && info.Mode().Perm()&0o111 == 0 {
		return Manifest{}, fmt.Errorf("plugin.path %q must be executable", manifest.Plugin.Path)
	}
	provenance, err := readProvenance(manifest.Dir)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Source, manifest.SourceKind = provenance.Source, provenance.Kind
	return manifest, nil
}

func Discover(pluginDir string) ([]Manifest, []ManifestIssue) {
	pluginDir = strings.TrimSpace(pluginDir)
	if pluginDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []ManifestIssue{{Path: pluginDir, Err: err}}
	}

	manifests := make([]Manifest, 0, len(entries))
	var issues []ManifestIssue
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		manifest, issue := loadInstalled(filepath.Join(pluginDir, entry.Name()))
		if issue != nil {
			if !os.IsNotExist(issue.Err) {
				issues = append(issues, *issue)
			}
			continue
		}
		manifests = append(manifests, manifest)
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Name < manifests[j].Name
	})
	return manifests, issues
}

func loadInstalled(entryPath string) (Manifest, *ManifestIssue) {
	linkTarget := ""
	info, err := os.Lstat(entryPath)
	if err != nil {
		return Manifest{}, &ManifestIssue{Path: entryPath, Err: err}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := linkedPluginTarget(entryPath)
		if err != nil {
			return Manifest{}, &ManifestIssue{Path: entryPath, Err: err}
		}
		linkTarget = target
	}
	manifestPath := filepath.Join(entryPath, ManifestName)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return Manifest{}, &ManifestIssue{Path: manifestPath, Err: err}
	}
	manifest.LinkTarget = linkTarget
	if linkTarget != "" {
		manifest.Source, manifest.SourceKind = "", ""
	}
	return manifest, nil
}

func installedPlugin(pluginDir, name string) (Manifest, error) {
	name = strings.TrimSpace(name)
	if !validInstallName(name) {
		return Manifest{}, fmt.Errorf("invalid plugin name %q", name)
	}
	manifest, issue := loadInstalled(filepath.Join(pluginDir, name))
	if issue != nil {
		if os.IsNotExist(issue.Err) {
			return Manifest{}, fmt.Errorf("plugin %q is not installed", name)
		}
		return Manifest{}, *issue
	}
	if manifest.Name != name {
		return Manifest{}, fmt.Errorf("plugin directory %q holds plugin %q", name, manifest.Name)
	}
	return manifest, nil
}

type SwapHandle struct {
	target string
	backup string
}

func (h *SwapHandle) Commit() error {
	if h.backup == "" {
		return nil
	}
	if err := os.RemoveAll(h.backup); err != nil {
		return fmt.Errorf("remove previous plugin copy: %w", err)
	}
	return nil
}

func (h *SwapHandle) Rollback() error {
	if err := os.RemoveAll(h.target); err != nil {
		return fmt.Errorf("remove replacement plugin: %w", err)
	}
	if h.backup == "" {
		return nil
	}
	if err := os.Rename(h.backup, h.target); err != nil {
		return fmt.Errorf("restore previous plugin: %w", err)
	}
	return nil
}

func swapIn(pluginDir, name string, place func(target string) error) (*SwapHandle, error) {
	target := filepath.Join(pluginDir, name)
	if err := removeStaleBackups(pluginDir, name); err != nil {
		return nil, err
	}
	handle := &SwapHandle{target: target}
	if _, err := os.Lstat(target); err == nil {
		handle.backup = filepath.Join(pluginDir, fmt.Sprintf(".%s.old-%d", name, time.Now().UnixNano()))
		if err := os.Rename(target, handle.backup); err != nil {
			return nil, fmt.Errorf("set aside plugin %q: %w", name, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect plugin %q: %w", name, err)
	}
	if err := place(target); err != nil {
		if handle.backup != "" {
			if restoreErr := os.Rename(handle.backup, target); restoreErr != nil {
				return nil, fmt.Errorf("publish plugin %q: %w; restore previous plugin: %v", name, err, restoreErr)
			}
		}
		return nil, fmt.Errorf("publish plugin %q: %w", name, err)
	}
	return handle, nil
}

func removeStaleBackups(pluginDir, name string) error {
	stale, err := filepath.Glob(filepath.Join(pluginDir, "."+name+".old-*"))
	if err != nil {
		return err
	}
	for _, path := range stale {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove stale plugin copy %s: %w", path, err)
		}
	}
	return nil
}

type stagedPlugin struct {
	manifest Manifest
	place    func(target string) error
	discard  func()
}

func (s stagedPlugin) publish(pluginDir string) (Manifest, *SwapHandle, error) {
	handle, err := swapIn(pluginDir, s.manifest.Name, s.place)
	if err != nil {
		return Manifest{}, nil, err
	}
	installed, issue := loadInstalled(handle.target)
	if issue != nil {
		if rollbackErr := handle.Rollback(); rollbackErr != nil {
			return Manifest{}, nil, fmt.Errorf("validate published plugin: %w; %v", *issue, rollbackErr)
		}
		return Manifest{}, nil, fmt.Errorf("validate published plugin: %w", *issue)
	}
	return installed, handle, nil
}

func (s stagedPlugin) installNew(pluginDir string) (Manifest, error) {
	defer s.discard()
	if _, err := os.Lstat(filepath.Join(pluginDir, s.manifest.Name)); err == nil {
		return Manifest{}, fmt.Errorf("plugin %q is already installed", s.manifest.Name)
	}
	installed, handle, err := s.publish(pluginDir)
	if err != nil {
		return Manifest{}, err
	}
	return installed, handle.Commit()
}

func (s stagedPlugin) replace(pluginDir string) (Manifest, *SwapHandle, error) {
	defer s.discard()
	return s.publish(pluginDir)
}

func stageSource(ctx context.Context, source, pluginDir string, opts InstallOptions) (stagedPlugin, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return stagedPlugin{}, errors.New("plugin source is required")
	}
	switch {
	case isGitSource(source):
		cloneRoot, err := os.MkdirTemp("", "attn-plugin-source-*")
		if err != nil {
			return stagedPlugin{}, fmt.Errorf("create plugin clone directory: %w", err)
		}
		defer os.RemoveAll(cloneRoot)
		checkoutDir := filepath.Join(cloneRoot, "checkout")
		if err := cloneGitSource(ctx, source, checkoutDir, opts); err != nil {
			return stagedPlugin{}, err
		}
		commit, err := gitHead(ctx, checkoutDir, opts.Env)
		if err != nil {
			return stagedPlugin{}, err
		}
		return stageDir(checkoutDir, pluginDir, Provenance{Source: redactGitSource(source), Kind: SourceKindGit, Commit: commit}, opts)
	case isArchiveSource(source):
		archive, err := filepath.Abs(source)
		if err != nil {
			return stagedPlugin{}, fmt.Errorf("resolve plugin archive: %w", err)
		}
		extractRoot, err := os.MkdirTemp("", "attn-plugin-archive-*")
		if err != nil {
			return stagedPlugin{}, fmt.Errorf("create plugin archive directory: %w", err)
		}
		defer os.RemoveAll(extractRoot)
		if err := extractArchive(archive, extractRoot); err != nil {
			return stagedPlugin{}, err
		}
		root, err := archivePluginRoot(extractRoot)
		if err != nil {
			return stagedPlugin{}, err
		}
		return stageDir(root, pluginDir, Provenance{Source: archive, Kind: SourceKindArchive}, opts)
	default:
		sourceDir, err := filepath.Abs(source)
		if err != nil {
			return stagedPlugin{}, fmt.Errorf("resolve source directory: %w", err)
		}
		return stageDir(sourceDir, pluginDir, Provenance{Source: sourceDir, Kind: SourceKindPath}, opts)
	}
}

func stageDir(sourceDir, pluginDir string, provenance Provenance, opts InstallOptions) (stagedPlugin, error) {
	sourceManifest, err := LoadManifest(filepath.Join(sourceDir, ManifestName))
	if err != nil {
		return stagedPlugin{}, fmt.Errorf("load source manifest: %w", err)
	}
	if !validInstallName(sourceManifest.Name) {
		return stagedPlugin{}, fmt.Errorf("name %q cannot be used as an install directory", sourceManifest.Name)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return stagedPlugin{}, fmt.Errorf("create plugin directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(pluginDir, "."+sourceManifest.Name+".install-*")
	if err != nil {
		return stagedPlugin{}, fmt.Errorf("create staging install directory: %w", err)
	}
	staged := stagedPlugin{
		place:   func(target string) error { return os.Rename(stagingDir, target) },
		discard: func() { _ = os.RemoveAll(stagingDir) },
	}
	if err := copyTree(sourceDir, stagingDir); err != nil {
		staged.discard()
		return stagedPlugin{}, err
	}
	provenance.InstalledAt = time.Now().UTC()
	if err := writeProvenance(stagingDir, provenance); err != nil {
		staged.discard()
		return stagedPlugin{}, err
	}
	staged.manifest, err = LoadManifest(filepath.Join(stagingDir, ManifestName))
	if err != nil {
		staged.discard()
		return stagedPlugin{}, fmt.Errorf("validate installed manifest: %w", err)
	}
	if err := installDependencies(stagingDir, opts.Env); err != nil {
		staged.discard()
		return stagedPlugin{}, err
	}
	return staged, nil
}

func InstallSourceWithOptions(source, pluginDir string, opts InstallOptions) (Manifest, error) {
	staged, err := stageSource(context.Background(), source, pluginDir, opts)
	if err != nil {
		return Manifest{}, err
	}
	return staged.installNew(pluginDir)
}

func ReplaceSourceWithOptionsContext(ctx context.Context, source, pluginDir string, opts InstallOptions) (Manifest, *SwapHandle, error) {
	staged, err := stageSource(ctx, source, pluginDir, opts)
	if err != nil {
		return Manifest{}, nil, err
	}
	return staged.replace(pluginDir)
}

func UpdateWithOptionsContext(ctx context.Context, name, pluginDir string, opts InstallOptions) (Manifest, *SwapHandle, error) {
	current, err := installedPlugin(pluginDir, name)
	if err != nil {
		return Manifest{}, nil, err
	}
	if current.LinkTarget != "" {
		return Manifest{}, nil, fmt.Errorf("plugin %q is linked to %s; use reload to pick up its changes", current.Name, current.LinkTarget)
	}
	if current.Source == "" {
		return Manifest{}, nil, fmt.Errorf("plugin %q has no recorded source; install it again from its source", current.Name)
	}
	staged, err := stageSource(ctx, current.Source, pluginDir, opts)
	if err != nil {
		return Manifest{}, nil, err
	}
	if staged.manifest.Name != current.Name {
		staged.discard()
		return Manifest{}, nil, fmt.Errorf("source %s now holds plugin %q, not %q", current.Source, staged.manifest.Name, current.Name)
	}
	return staged.replace(pluginDir)
}

type ReloadOptions struct {
	Env         []string
	SkipInstall bool
}

func PrepareReload(name, pluginDir string, opts ReloadOptions) (Manifest, error) {
	manifest, err := installedPlugin(pluginDir, name)
	if err != nil {
		return Manifest{}, err
	}
	if !opts.SkipInstall {
		if err := installDependencies(manifest.Dir, opts.Env); err != nil {
			return Manifest{}, err
		}
	}
	return manifest, nil
}

func LinkPath(sourceDir, pluginDir string, opts InstallOptions) (Manifest, error) {
	staged, err := stageLink(sourceDir, pluginDir, opts)
	if err != nil {
		return Manifest{}, err
	}
	return staged.installNew(pluginDir)
}

func ReplaceLinkPath(sourceDir, pluginDir string, opts InstallOptions) (Manifest, *SwapHandle, error) {
	staged, err := stageLink(sourceDir, pluginDir, opts)
	if err != nil {
		return Manifest{}, nil, err
	}
	return staged.replace(pluginDir)
}

func stageLink(sourceDir, pluginDir string, opts InstallOptions) (stagedPlugin, error) {
	sourceDir, err := filepath.Abs(strings.TrimSpace(sourceDir))
	if err != nil {
		return stagedPlugin{}, fmt.Errorf("resolve source directory: %w", err)
	}
	sourceManifest, err := LoadManifest(filepath.Join(sourceDir, ManifestName))
	if err != nil {
		return stagedPlugin{}, fmt.Errorf("load source manifest: %w", err)
	}
	if !validInstallName(sourceManifest.Name) {
		return stagedPlugin{}, fmt.Errorf("name %q cannot be used as an install directory", sourceManifest.Name)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return stagedPlugin{}, fmt.Errorf("create plugin directory: %w", err)
	}
	if pathWithinDir(pluginDir, sourceDir) {
		return stagedPlugin{}, fmt.Errorf("source %q is inside the plugin directory; use install instead", sourceDir)
	}
	if err := installDependencies(sourceDir, opts.Env); err != nil {
		return stagedPlugin{}, err
	}
	return stagedPlugin{
		manifest: sourceManifest,
		place:    func(target string) error { return os.Symlink(sourceDir, target) },
		discard:  func() {},
	}, nil
}

type SourceKind string

const (
	SourceKindGit     SourceKind = "git"
	SourceKindPath    SourceKind = "path"
	SourceKindArchive SourceKind = "archive"
)

const ProvenanceName = ".attn-source.json"

type Provenance struct {
	Source      string     `json:"source"`
	Kind        SourceKind `json:"kind"`
	InstalledAt time.Time  `json:"installed_at"`
	Commit      string     `json:"commit,omitempty"`
}

func writeProvenance(dir string, provenance Provenance) error {
	body, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plugin provenance: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProvenanceName), append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write plugin provenance: %w", err)
	}
	return nil
}

func readProvenance(dir string) (Provenance, error) {
	body, err := os.ReadFile(filepath.Join(dir, ProvenanceName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Provenance{}, nil
		}
		return Provenance{}, fmt.Errorf("read plugin provenance: %w", err)
	}
	var provenance Provenance
	if err := json.Unmarshal(body, &provenance); err != nil {
		return Provenance{}, fmt.Errorf("decode %s: %w", ProvenanceName, err)
	}
	return provenance, nil
}

func gitHead(ctx context.Context, checkoutDir string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/env", "git", "-C", checkoutDir, "rev-parse", "HEAD")
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read cloned plugin commit: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func isArchiveSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz")
}

func extractArchive(archivePath, targetDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open plugin archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read plugin archive: %w", err)
	}
	defer compressed.Close()
	entries := tar.NewReader(compressed)
	for {
		header, err := entries.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read plugin archive: %w", err)
		}
		name := header.Name
		if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
			return fmt.Errorf("plugin archive entry %q has an absolute path", name)
		}
		targetPath := filepath.Join(targetDir, filepath.FromSlash(name))
		if !pathWithinDir(targetDir, targetPath) {
			return fmt.Errorf("plugin archive entry %q leaves the archive", name)
		}
		mode := header.FileInfo().Mode().Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, mode|0o700); err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
			if err := writeArchiveFile(entries, targetPath, mode); err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("plugin archive entry %q is a link; links are not supported in plugin installs", name)
		case tar.TypeXGlobalHeader:
		default:
			return fmt.Errorf("plugin archive entry %q has an unsupported type", name)
		}
	}
}

func writeArchiveFile(source io.Reader, targetPath string, mode fs.FileMode) error {
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

func archivePluginRoot(extractRoot string) (string, error) {
	if _, err := os.Stat(filepath.Join(extractRoot, ManifestName)); err == nil {
		return extractRoot, nil
	}
	entries, err := os.ReadDir(extractRoot)
	if err != nil {
		return "", fmt.Errorf("inspect plugin archive: %w", err)
	}
	if len(entries) == 1 && entries[0].IsDir() {
		root := filepath.Join(extractRoot, entries[0].Name())
		if _, err := os.Stat(filepath.Join(root, ManifestName)); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("plugin archive holds no %s at its root or inside a single top-level directory", ManifestName)
}
func linkedPluginTarget(linkPath string) (string, error) {
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("linked plugin target %q: %w", target, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("linked plugin target %q is not a directory", target)
	}
	return filepath.Clean(target), nil
}

func isGitSource(source string) bool {
	if strings.HasPrefix(source, "git@") {
		return strings.Contains(source, ":")
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Scheme {
	case "git", "http", "https", "ssh":
		return true
	default:
		return false
	}
}

func cloneGitSource(ctx context.Context, source, targetDir string, opts InstallOptions) error {
	clone := opts.CloneGit
	if clone == nil {
		clone = directGitClone
	}
	output, err := clone(ctx, source, targetDir, opts.Env)
	if err == nil {
		return nil
	}
	details := strings.TrimSpace(redactGitOutput(string(output), source))
	if details == "" {
		return fmt.Errorf("clone plugin repository: %w", err)
	}
	return fmt.Errorf("clone plugin repository: %w: %s", err, details)
}

func directGitClone(ctx context.Context, source, targetDir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/env", "git", "clone", "--depth", "1", source, targetDir)
	cmd.Env = env
	return cmd.CombinedOutput()
}

func redactGitOutput(output, source string) string {
	output = strings.ReplaceAll(output, source, redactGitSource(source))
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return output
	}
	for _, secret := range []string{parsed.User.String(), parsed.RawQuery, parsed.Fragment} {
		if secret != "" {
			output = strings.ReplaceAll(output, secret, "REDACTED")
		}
	}
	return output
}

func redactGitSource(source string) string {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return source
	}
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "REDACTED"
	}
	if parsed.Fragment != "" {
		parsed.Fragment = "REDACTED"
	}
	return parsed.String()
}

func Remove(pluginDir, name string) error {
	name = strings.TrimSpace(name)
	if !validInstallName(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	path := filepath.Join(pluginDir, name)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin %q is not installed", name)
		}
		return fmt.Errorf("inspect plugin %q: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("unlink plugin %q: %w", name, err)
		}
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove plugin %q: %w", name, err)
	}
	return nil
}

func validInstallName(name string) bool {
	return !strings.HasPrefix(name, ".") &&
		name == filepath.Base(name) &&
		!strings.ContainsAny(name, `/\`)
}

func pathWithinDir(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func installDependencies(pluginDir string, env []string) error {
	cmd := exec.Command("/usr/bin/env", "bun", "install")
	cmd.Dir = pluginDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			return fmt.Errorf("run bun install: %w", err)
		}
		return fmt.Errorf("run bun install: %w: %s", err, details)
	}
	return nil
}

func copyTree(sourceDir, targetDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("relative plugin path: %w", err)
		}
		if rel == "." {
			return os.MkdirAll(targetDir, 0o755)
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}

		targetPath := filepath.Join(targetDir, rel)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", rel, err)
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("copy %s: symlinks are not supported in plugin installs", rel)
		case entry.IsDir():
			if err := os.MkdirAll(targetPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create directory %s: %w", rel, err)
			}
			return nil
		case info.Mode().IsRegular():
			if err := copyRegularFile(path, targetPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("copy file %s: %w", rel, err)
			}
			return nil
		default:
			return fmt.Errorf("copy %s: unsupported file type", rel)
		}
	})
}

func copyRegularFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return nil
}
