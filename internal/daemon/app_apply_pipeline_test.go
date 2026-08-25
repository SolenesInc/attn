package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
)

// The builder and the daemon run in separate processes in production, so a divergence hides here.

func requireAppToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not on PATH; building an app needs it")
	}
}

func buildApp(t *testing.T, d *Daemon, dir string) appbuild.Result {
	t.Helper()
	res, err := appbuild.Build(context.Background(), appbuild.Options{Dir: dir, StoreDir: d.appsDir})
	if err != nil {
		t.Fatalf("build %s: %v", dir, err)
	}
	return res
}

func applyBuilt(t *testing.T, d *Daemon, res appbuild.Result) *protocol.AppApplyResult {
	t.Helper()
	resp := appApply(t, d, res.Manifest.Name, res.ContentHash, res.Declaration)
	if !resp.Ok {
		t.Fatalf("app apply %s: %v", res.Manifest.Name, protocol.Deref(resp.Error))
	}
	return resp.AppApplyResult
}

func TestAppApplyRecordsWhatTheBuilderProduced(t *testing.T) {
	requireAppToolchain(t)
	d := appApplyDaemon(t)
	dir := filepath.Join(t.TempDir(), "pipeline-app")
	if _, err := appbuild.Scaffold(appbuild.ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	built := buildApp(t, d, dir)
	first := applyBuilt(t, d, built)
	if !first.VersionCreated {
		t.Fatal("the first apply of a new app reused a row")
	}
	if first.ArtifactPath != built.ArtifactPath {
		t.Fatalf("daemon recorded %q, builder wrote %q", first.ArtifactPath, built.ArtifactPath)
	}

	second := applyBuilt(t, d, buildApp(t, d, dir))
	if second.VersionID != first.VersionID || second.VersionCreated {
		t.Fatalf("re-applying an untouched app gave %+v, want the same version reused", second)
	}
	if count, err := d.store.CountAppVersions("pipeline-app"); err != nil || count != 1 {
		t.Fatalf("versions = %d (%v), want 1", count, err)
	}

	entrypoint := filepath.Join(dir, "src", "index.ts")
	source, err := os.ReadFile(entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(source), "seq: event.seq,", "seq: event.seq, edited: true,", 1)
	if edited == string(source) {
		t.Fatal("the scaffold's entrypoint no longer contains the line this test edits")
	}
	if err := os.WriteFile(entrypoint, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	third := applyBuilt(t, d, buildApp(t, d, dir))
	if third.VersionID == first.VersionID || !third.VersionCreated {
		t.Fatalf("an edited app gave %+v, want a new version", third)
	}

	resp := appRollback(t, d, "pipeline-app", first.VersionID)
	if !resp.Ok {
		t.Fatalf("rollback: %v", protocol.Deref(resp.Error))
	}
	app, _, err := d.store.GetApp("pipeline-app")
	if err != nil {
		t.Fatal(err)
	}
	if app.CurrentVersionID != int64(first.VersionID) {
		t.Fatalf("pointer = %d, want %d", app.CurrentVersionID, first.VersionID)
	}
}

func TestAppApplyBrokenOverInstalledLeavesTheGoodVersionInPlace(t *testing.T) {
	requireAppToolchain(t)
	d := appApplyDaemon(t)
	dir := filepath.Join(t.TempDir(), "regressing-app")
	if _, err := appbuild.Scaffold(appbuild.ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	good := applyBuilt(t, d, buildApp(t, d, dir))

	manifest := filepath.Join(dir, appbuild.ManifestName)
	text, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(text),
		`events = ["session.state.changed"]`,
		`events = ["session.state.changed", "ticket.created"]`, 1)
	if err := os.WriteFile(manifest, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := appbuild.Build(context.Background(), appbuild.Options{Dir: dir, StoreDir: d.appsDir}); err == nil {
		t.Fatal("the broken app built")
	}

	app, _, err := d.store.GetApp("regressing-app")
	if err != nil {
		t.Fatal(err)
	}
	if app.CurrentVersionID != int64(good.VersionID) {
		t.Fatalf("pointer = %d, want the good version %d", app.CurrentVersionID, good.VersionID)
	}
	if count, err := d.store.CountAppVersions("regressing-app"); err != nil || count != 1 {
		t.Fatalf("versions = %d (%v), want only the good one", count, err)
	}
	entries, err := os.ReadDir(filepath.Join(d.appsDir, "regressing-app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the artifact store holds %d directories, want only the good version's", len(entries))
	}
	if _, err := os.Stat(good.ArtifactPath); err != nil {
		t.Fatalf("the good artifact is gone: %v", err)
	}
}
