package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/store"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newAppDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	if err := d.eventBus.Start(); err != nil {
		t.Fatalf("start the event bus: %v", err)
	}
	t.Cleanup(d.stopEventBus)
	return d
}

func installApp(t *testing.T, d *Daemon, name string, manifest appbuild.Manifest) store.AppVersion {
	t.Helper()
	manifest.Name = name
	if manifest.AttnAppAPI == 0 {
		manifest.AttnAppAPI = appbuild.APIVersion
	}
	if manifest.Entrypoint == "" {
		manifest.Entrypoint = "src/index.ts"
	}
	declaration, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal declaration: %v", err)
	}
	now := time.Now().UTC()
	if err := d.store.SaveApp(name, now); err != nil {
		t.Fatalf("save app %s: %v", name, err)
	}
	hash := fmt.Sprintf("sha256:%s-%x", name, len(declaration))
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  hash,
		Declaration:  string(declaration),
		ArtifactPath: filepath.Join("apps", name, hash+".js"),
	}, now)
	if err != nil {
		t.Fatalf("commit version for %s: %v", name, err)
	}
	if err := d.store.SetAppCurrentVersion(name, version.ID, now); err != nil {
		t.Fatalf("point %s at version %d: %v", name, version.ID, err)
	}
	d.syncAppRuntimeForVersion(name)
	return version
}

func subscribing(events ...string) appbuild.Manifest {
	m := subscribingWithoutReconcile(events...)
	m.Reconcile = true
	return m
}

func subscribingWithoutReconcile(events ...string) appbuild.Manifest {
	return appbuild.Manifest{Subscribe: []appbuild.Subscribe{{Events: events}}}
}

func invocationsOf(t *testing.T, d *Daemon, name string) []store.AppInvocation {
	t.Helper()
	rows, err := d.store.ListAppInvocations(name, 50)
	if err != nil {
		t.Fatalf("list invocations for %s: %v", name, err)
	}
	return rows
}

func TestAppRuntimeHostCandidates(t *testing.T) {
	bundled := appRuntimeHostCandidates("/Applications/attn.app/Contents/MacOS/attn", "")
	if len(bundled) != 2 || bundled[0] != "/Applications/attn.app/Contents/Resources/app-runtime/attn-app-runtime" {
		t.Fatalf("bundled candidates = %v", bundled)
	}

	remote := appRuntimeHostCandidates("/home/v/.local/bin/attn-dev", "dev")
	want := []string{
		"/home/v/.local/bin/attn-app-runtime-dev",
		"/home/v/.local/bin/attn-app-runtime",
	}
	if len(remote) != len(want) {
		t.Fatalf("instance candidates = %v, want %v", remote, want)
	}
	for i := range want {
		if remote[i] != want[i] {
			t.Fatalf("instance candidates = %v, want %v", remote, want)
		}
	}

	checkout := appRuntimeHostCandidates("/src/attn/attn", "dev")
	if checkout[len(checkout)-1] != "/src/attn/attn-app-runtime" {
		t.Fatalf("checkout candidates = %v", checkout)
	}
}
