package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type pluginWorld struct {
	t         *testing.T
	root      string
	pluginDir string
	bunLog    string
	opts      InstallOptions
}

func newPluginWorld(t *testing.T) *pluginWorld {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	w := &pluginWorld{t: t, root: root, pluginDir: filepath.Join(root, "plugins"), bunLog: filepath.Join(root, "bun.log")}
	fakeBun := "#!/bin/sh\necho \"$PWD\" >> " + w.bunLog + "\nif [ -e fail-install ]; then echo 'install exploded' >&2; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "bun"), []byte(fakeBun), 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	env := []string{"PATH=" + bin + ":" + filepath.Dir(gitPath) + ":/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1"}
	w.opts = InstallOptions{Env: env}
	return w
}

func (w *pluginWorld) install(source string) error {
	_, swap, err := ReplaceSourceWithOptionsContext(context.Background(), source, w.pluginDir, w.opts)
	if err != nil {
		return err
	}
	return swap.Commit()
}

func (w *pluginWorld) link(source string) error {
	_, swap, err := ReplaceLinkPath(source, w.pluginDir, w.opts)
	if err != nil {
		return err
	}
	return swap.Commit()
}

func (w *pluginWorld) checkout(rel, name, version string) string {
	w.t.Helper()
	dir := filepath.Join(w.root, rel)
	writePluginFiles(w.t, dir, name, version)
	return dir
}

func writePluginFiles(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\nattn_api_version = 7\n\n[plugin]\nentrypoint = \"index.ts\"\n"
	for file, body := range map[string]string{ManifestName: manifest, "index.ts": "// " + version + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (w *pluginWorld) installedVersion(name string) string {
	w.t.Helper()
	found, issues := Discover(w.pluginDir)
	if len(issues) != 0 {
		w.t.Fatalf("Discover issues: %v", issues)
	}
	for _, manifest := range found {
		if manifest.Name == name {
			return manifest.Version
		}
	}
	return ""
}

func (w *pluginWorld) leftovers() []string {
	w.t.Helper()
	entries, err := os.ReadDir(w.pluginDir)
	if err != nil {
		w.t.Fatal(err)
	}
	var hidden []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			hidden = append(hidden, entry.Name())
		}
	}
	return hidden
}

func (w *pluginWorld) bunRuns() int {
	body, err := os.ReadFile(w.bunLog)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Count(string(body), "\n")
}

func TestReplacingAPluginSwapsInTheNewCopyAndRollbackRestoresThePreviousOne(t *testing.T) {
	w := newPluginWorld(t)
	v1 := w.checkout("v1", "sample", "0.1.0")
	v2 := w.checkout("v2", "sample", "0.2.0")
	if err := w.install(v1); err != nil {
		t.Fatal(err)
	}

	replaced, swap, err := ReplaceSourceWithOptionsContext(context.Background(), v2, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Version != "0.2.0" || replaced.Dir != filepath.Join(w.pluginDir, "sample") || w.installedVersion("sample") != "0.2.0" {
		t.Fatalf("after replace: returned %+v, discovered %q; want 0.2.0 live at the plugin's own directory", replaced, w.installedVersion("sample"))
	}
	if err := swap.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := w.installedVersion("sample"); got != "0.1.0" {
		t.Fatalf("after rollback the live plugin is %q, want the previous 0.1.0", got)
	}
	if left := w.leftovers(); len(left) != 0 {
		t.Fatalf("rollback left %v in the plugin directory", left)
	}

	_, swap, err = ReplaceSourceWithOptionsContext(context.Background(), v2, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, left := w.installedVersion("sample"), w.leftovers(); got != "0.2.0" || len(left) != 0 {
		t.Fatalf("after commit: live %q, leftovers %v; want 0.2.0 and no previous copy", got, left)
	}

	linkedCheckout := w.checkout("linked", "sample", "0.3.0-dev")
	linked, swap, err := ReplaceLinkPath(linkedCheckout, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Commit(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(w.pluginDir, "sample")); err != nil || info.Mode()&os.ModeSymlink == 0 || linked.LinkTarget != linkedCheckout || linked.Source != "" {
		t.Fatalf("after linking: %+v (%v); want a symlink to %s with no managed source", linked, err, linkedCheckout)
	}

	managed, swap, err := ReplaceSourceWithOptionsContext(context.Background(), v1, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(w.pluginDir, "sample"))
	if err != nil || !info.IsDir() || managed.LinkTarget != "" || managed.Version != "0.1.0" {
		t.Fatalf("after reinstalling a managed copy: %+v (%v); want a real directory holding 0.1.0", managed, err)
	}
	if _, err := os.Stat(filepath.Join(linkedCheckout, ManifestName)); err != nil {
		t.Fatalf("replacing the link removed the linked checkout: %v", err)
	}
}

func TestAReplacementThatFailsToStageLeavesTheLivePluginUntouched(t *testing.T) {
	w := newPluginWorld(t)
	if err := w.install(w.checkout("v1", "sample", "0.1.0")); err != nil {
		t.Fatal(err)
	}

	failing := w.checkout("failing", "sample", "0.2.0")
	if err := os.WriteFile(filepath.Join(failing, "fail-install"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	invalid := w.checkout("invalid", "sample", "0.2.0")
	if err := os.Remove(filepath.Join(invalid, "index.ts")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		replace func() error
		refusal string
	}{
		{"bun install fails", func() error {
			_, _, err := ReplaceSourceWithOptionsContext(context.Background(), failing, w.pluginDir, w.opts)
			return err
		}, "install exploded"},
		{"the manifest is invalid", func() error {
			_, _, err := ReplaceSourceWithOptionsContext(context.Background(), invalid, w.pluginDir, w.opts)
			return err
		}, "plugin.path"},
		{"a linked checkout fails bun install", func() error {
			_, _, err := ReplaceLinkPath(failing, w.pluginDir, w.opts)
			return err
		}, "install exploded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.replace(); err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("replace = %v, want a refusal naming %q", err, tc.refusal)
			}
			if got, left := w.installedVersion("sample"), w.leftovers(); got != "0.1.0" || len(left) != 0 {
				t.Fatalf("after the refused replace: live %q, leftovers %v; want 0.1.0 untouched and nothing staged left", got, left)
			}
		})
	}
}

type archiveEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func writeArchive(t *testing.T, path string, entries []archiveEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Linkname: entry.linkname, Mode: 0o644, Size: int64(len(entry.body))}
		if entry.typeflag == tar.TypeDir {
			header.Mode, header.Size = 0o755, 0
		}
		if entry.typeflag == tar.TypeSymlink {
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pluginArchiveEntries(prefix, version string) []archiveEntry {
	manifest := "name = \"sample\"\nversion = \"" + version + "\"\nattn_api_version = 7\n\n[plugin]\nentrypoint = \"index.ts\"\n"
	return []archiveEntry{
		{name: prefix + ManifestName, body: manifest, typeflag: tar.TypeReg},
		{name: prefix + "index.ts", body: "// entrypoint\n", typeflag: tar.TypeReg},
	}
}

func TestAPluginInstallsFromAnArchiveAndRefusesEntriesThatLeaveIt(t *testing.T) {
	w := newPluginWorld(t)
	rootLevel := writeArchive(t, filepath.Join(w.root, "root-level.tgz"), pluginArchiveEntries("", "0.1.0"))
	nested := writeArchive(t, filepath.Join(w.root, "nested.tar.gz"), append([]archiveEntry{{name: "package/", typeflag: tar.TypeDir}}, pluginArchiveEntries("package/", "0.2.0")...))

	installed, swap, err := ReplaceSourceWithOptionsContext(context.Background(), rootLevel, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = swap.Commit()
	if installed.Version != "0.1.0" || installed.SourceKind != SourceKindArchive || installed.Source != rootLevel {
		t.Fatalf("root-level archive installed %+v, want 0.1.0 recorded as archive %s", installed, rootLevel)
	}
	installed, swap, err = ReplaceSourceWithOptionsContext(context.Background(), nested, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = swap.Commit()
	if installed.Version != "0.2.0" || installed.Source != nested {
		t.Fatalf("archive with one top-level directory installed %+v, want 0.2.0 from %s", installed, nested)
	}

	outside := filepath.Join(w.root, "escaped")
	for _, tc := range []struct {
		name    string
		entries []archiveEntry
		refusal string
	}{
		{"a parent-directory path", append(pluginArchiveEntries("", "0.9.0"), archiveEntry{name: "../escaped", body: "x", typeflag: tar.TypeReg}), "leaves the archive"},
		{"an absolute path", append(pluginArchiveEntries("", "0.9.0"), archiveEntry{name: outside, body: "x", typeflag: tar.TypeReg}), "absolute path"},
		{"a symlink", append(pluginArchiveEntries("", "0.9.0"), archiveEntry{name: "escape", typeflag: tar.TypeSymlink, linkname: outside}), "links are not supported"},
		{"no manifest", []archiveEntry{{name: "a/x", body: "x", typeflag: tar.TypeReg}, {name: "b/y", body: "y", typeflag: tar.TypeReg}}, "holds no " + ManifestName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeArchive(t, filepath.Join(w.root, strings.ReplaceAll(tc.name, " ", "-")+".tgz"), tc.entries)
			if _, _, err := ReplaceSourceWithOptionsContext(context.Background(), archive, w.pluginDir, w.opts); err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("installing the archive = %v, want a refusal naming %q", err, tc.refusal)
			}
			if _, err := os.Lstat(outside); !os.IsNotExist(err) {
				t.Fatalf("the refused archive wrote %s (%v)", outside, err)
			}
			if got := w.installedVersion("sample"); got != "0.2.0" {
				t.Fatalf("the refused archive changed the live plugin to %q", got)
			}
		})
	}
}

func gitIn(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestAGitInstallRecordsItsRedactedSourceAndCommitAndUpdatesFromThem(t *testing.T) {
	w := newPluginWorld(t)
	repo := filepath.Join(w.root, "repo")
	writePluginFiles(t, repo, "sample", "0.1.0")
	gitIn(t, w.opts.Env, repo, "init", "-q", "-b", "main")
	gitIn(t, w.opts.Env, repo, "add", ".")
	gitIn(t, w.opts.Env, repo, "commit", "-q", "-m", "v1")
	firstCommit := gitIn(t, w.opts.Env, repo, "rev-parse", "HEAD")

	var cloned []string
	w.opts.CloneGit = func(ctx context.Context, source, target string, env []string) ([]byte, error) {
		cloned = append(cloned, source)
		return exec.CommandContext(ctx, "git", "clone", "-q", "--depth", "1", "file://"+repo, target).CombinedOutput()
	}
	source := "ssh://git@git.example.com/team/sample.git"
	installed, swap, err := ReplaceSourceWithOptionsContext(context.Background(), source, w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = swap.Commit()

	body, err := os.ReadFile(filepath.Join(w.pluginDir, "sample", ProvenanceName))
	if err != nil {
		t.Fatal(err)
	}
	var recorded Provenance
	if err := json.Unmarshal(body, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Kind != SourceKindGit || recorded.Commit != firstCommit || recorded.InstalledAt.IsZero() || recorded.Source != source {
		t.Fatalf("%s = %s; want git source %s, commit %s and an install time", ProvenanceName, body, source, firstCommit)
	}
	discovered, _ := Discover(w.pluginDir)
	if len(discovered) != 1 || discovered[0].Source != recorded.Source || discovered[0].SourceKind != SourceKindGit || installed.Source != recorded.Source {
		t.Fatalf("Discover = %+v, install returned %+v; want both to carry the recorded source", discovered, installed)
	}

	writePluginFiles(t, repo, "sample", "0.2.0")
	gitIn(t, w.opts.Env, repo, "commit", "-q", "-am", "v2")
	updated, swap, err := UpdateWithOptionsContext(context.Background(), "sample", w.pluginDir, w.opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = swap.Commit()
	updatedProvenance, _ := readProvenance(updated.Dir)
	if updated.Version != "0.2.0" || updatedProvenance.Commit != gitIn(t, w.opts.Env, repo, "rev-parse", "HEAD") || cloned[len(cloned)-1] != recorded.Source {
		t.Fatalf("update installed %+v at %+v from %q; want 0.2.0 at the new commit, fetched from the recorded source", updated, updatedProvenance, cloned[len(cloned)-1])
	}

	writePluginFiles(t, repo, "renamed", "0.3.0")
	gitIn(t, w.opts.Env, repo, "commit", "-q", "-am", "rename")
	if _, _, err := UpdateWithOptionsContext(context.Background(), "sample", w.pluginDir, w.opts); err == nil || !strings.Contains(err.Error(), `holds plugin "renamed"`) {
		t.Fatalf("updating from a source that renamed the plugin = %v, want a refusal", err)
	}
	if got, left := w.installedVersion("sample"), w.leftovers(); got != "0.2.0" || len(left) != 0 {
		t.Fatalf("the refused update left live %q and %v; want 0.2.0 untouched", got, left)
	}
}

func TestAGitSourceWithCredentialsIsRecordedWithoutThemAndCannotBeUpdated(t *testing.T) {
	w := newPluginWorld(t)
	repo := filepath.Join(w.root, "repo")
	writePluginFiles(t, repo, "sample", "0.1.0")
	gitIn(t, w.opts.Env, repo, "init", "-q", "-b", "main")
	gitIn(t, w.opts.Env, repo, "add", ".")
	gitIn(t, w.opts.Env, repo, "commit", "-q", "-m", "v1")
	cloned := 0
	w.opts.CloneGit = func(ctx context.Context, source, target string, env []string) ([]byte, error) {
		cloned++
		return exec.CommandContext(ctx, "git", "clone", "-q", "--depth", "1", "file://"+repo, target).CombinedOutput()
	}
	for _, source := range []string{
		"https://user:hunter2@git.example.com/team/sample.git",
		"https://ghp_hunter2@git.example.com/team/sample.git",
		"https://git.example.com/team/sample.git?access_token=hunter2",
	} {
		t.Run(source, func(t *testing.T) {
			_, swap, err := ReplaceSourceWithOptionsContext(context.Background(), source, w.pluginDir, w.opts)
			if err != nil {
				t.Fatal(err)
			}
			_ = swap.Commit()
			body, err := os.ReadFile(filepath.Join(w.pluginDir, "sample", ProvenanceName))
			if err != nil || strings.Contains(string(body), "hunter2") || !strings.Contains(string(body), "git.example.com/team/sample.git") {
				t.Fatalf("%s = %s (%v); want the source without its credentials", ProvenanceName, body, err)
			}
			before := cloned
			if _, _, err := UpdateWithOptionsContext(context.Background(), "sample", w.pluginDir, w.opts); err == nil || !strings.Contains(err.Error(), "without its credentials") || cloned != before {
				t.Fatalf("updating a plugin recorded without its credentials = %v after %d clones, want a refusal before cloning", err, cloned-before)
			}
		})
	}
}

func TestUpdateRefusesPluginsItCannotFetchAgain(t *testing.T) {
	w := newPluginWorld(t)
	linked := w.checkout("linked", "linked", "0.1.0")
	if err := w.link(linked); err != nil {
		t.Fatal(err)
	}
	writePluginFiles(t, filepath.Join(w.pluginDir, "unrecorded"), "unrecorded", "0.1.0")

	for name, refusal := range map[string]string{
		"linked":     "use reload",
		"unrecorded": "no recorded source",
		"missing":    "is not installed",
	} {
		if _, _, err := UpdateWithOptionsContext(context.Background(), name, w.pluginDir, w.opts); err == nil || !strings.Contains(err.Error(), refusal) {
			t.Errorf("updating %s = %v, want a refusal naming %q", name, err, refusal)
		}
	}
}

func TestPrepareReloadValidatesThePluginAndRefreshesDependenciesUnlessSkipped(t *testing.T) {
	w := newPluginWorld(t)
	checkout := w.checkout("linked", "sample", "0.1.0")
	if err := w.link(checkout); err != nil {
		t.Fatal(err)
	}
	before := w.bunRuns()

	writePluginFiles(t, checkout, "sample", "0.2.0")
	reloaded, err := PrepareReload("sample", w.pluginDir, ReloadOptions{Env: w.opts.Env})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Version != "0.2.0" || reloaded.LinkTarget != checkout || w.bunRuns() != before+1 {
		t.Fatalf("reload = %+v after %d bun runs; want 0.2.0 linked to %s and one bun install", reloaded, w.bunRuns()-before, checkout)
	}
	if _, err := PrepareReload("sample", w.pluginDir, ReloadOptions{Env: w.opts.Env, SkipInstall: true}); err != nil || w.bunRuns() != before+1 {
		t.Fatalf("reload without install = %v after %d bun runs; want success with no bun install", err, w.bunRuns()-before)
	}

	if err := os.Remove(filepath.Join(checkout, "index.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareReload("sample", w.pluginDir, ReloadOptions{Env: w.opts.Env}); err == nil || !strings.Contains(err.Error(), "plugin.path") {
		t.Fatalf("reloading a broken manifest = %v, want its validation error", err)
	}
}
