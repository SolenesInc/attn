package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAppApplyRefusesAVersionItCannotVerifyAndRecordsNothing(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	plain := appManifestDeclaration(t, appbuild.Manifest{Name: "approval-gate"})
	withView := appManifestDeclaration(t, appbuild.Manifest{Name: "approval-gate", Views: []appbuild.View{appTileView("approvals", "Pending")}})
	another := appManifestDeclaration(t, appbuild.Manifest{Name: "standup-digest"})
	approvals := appbuild.ViewArtifact{Name: "approvals", Content: []byte("export default function A(){}\n")}

	for _, tc := range []struct {
		name        string
		declaration string
		stage       func() string
		want        []string
		namesHash   bool
	}{
		{"a bundle that does not hash to its version", plain, func() string {
			hash := stageAppVersion(t, "approval-gate", plain, "export default {}\n")
			writeAppArtifact(t, appbuild.ArtifactPath(config.AppsDir(), "approval-gate", hash), []byte("tampered\n"))
			return hash
		}, []string{"nothing was recorded"}, true},
		{"a version with no artifact", plain, func() string {
			return appbuild.VersionHash(plain, []byte("never written"), nil)
		}, []string{config.AppsDir()}, false},
		{"a view that does not hash to its version", withView, func() string {
			hash := stageAppVersion(t, "approval-gate", withView, "export default {}\n", approvals)
			writeAppArtifact(t, appbuild.ViewArtifactPath(config.AppsDir(), "approval-gate", hash, "approvals"), []byte("export default function B(){}\n"))
			return hash
		}, []string{"nothing was recorded"}, false},
		{"a declared view with no artifact", withView, func() string {
			return stageAppVersion(t, "approval-gate", withView, "export default { view: false }\n")
		}, []string{"approvals", "views/approvals.js"}, false},
		{"a declaration naming another app", another, func() string {
			return stageAppVersion(t, "approval-gate", another, "export default {}\n")
		}, []string{"standup-digest"}, false},
		{"a content hash that is not one", plain, func() string { return "not-a-hash" }, []string{"hex"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hash := tc.stage()
			_, err := cli.AppApply("approval-gate", hash, tc.declaration, "")
			if err == nil {
				t.Fatal("the apply was accepted")
			}
			want := tc.want
			if tc.namesHash {
				want = append(want, hash)
			}
			for _, text := range want {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("the refusal does not say %q: %v", text, err)
				}
			}
			if got := appNames(t, cli); got != "" {
				t.Errorf("apps after the refusal = %q, want none", got)
			}
		})
	}
}

func TestApplyRefusesAReservedAppName(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	for _, name := range apps.ReservedNames() {
		declaration := fmt.Sprintf(`{"name":%q,"subscribe":[{"events":["ticket.*"]}]}`, name)
		if _, err := cli.AppApply(name, "sha256:whatever", declaration, ""); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("applying an app called %q = %v, want it refused as reserved", name, err)
		}
	}
	if got := appNames(t, cli); got != "" {
		t.Errorf("apps = %q, want none installed", got)
	}
}

func TestANewVersionOfASubscribedAppMustReconcile(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	legacy := appManifestDeclaration(t, appbuild.Manifest{Name: "greeter", Subscribe: []appbuild.Subscribe{{Events: []string{"ticket.*"}}}})
	first := applyDeclaration(t, cli, "greeter", legacy, "first")

	edited := stageAppVersion(t, "greeter", legacy, "export default {} // edited")
	_, err := cli.AppApply("greeter", edited, legacy, "")
	if err == nil {
		t.Fatal("a subscribed version with no reconcile handler was applied over an existing one")
	}
	for _, want := range []string{"greeter", "reconcile = true", fmt.Sprintf("version %d", first.VersionID)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	status := appStatus(t, cli, "greeter")
	if servingVersion(status) != first.VersionID || status.Versions != 1 {
		t.Errorf("after the refusal: serving %d of %d versions, want the first of one", servingVersion(status), status.Versions)
	}
	if status.Reconcile.State == "owed" {
		t.Errorf("a refused move left a reconcile owed: %+v", status.Reconcile)
	}
}

func TestAppRollbackRefusalsSayWhatTheUserCanRollBackTo(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	only := applyApp(t, cli, "approval-gate", "first")
	requireAppRollbackRefusal(t, cli, "approval-gate", 0, "oldest version in its serving history", fmt.Sprint(only.VersionID), "current")

	theirs := applyApp(t, cli, "standup-digest", "first")
	requireAppRollbackRefusal(t, cli, "approval-gate", theirs.VersionID, "not a version of this app", fmt.Sprint(only.VersionID), "current")

	current := applyApp(t, cli, "approval-gate", "second")
	requireAppRollbackRefusal(t, cli, "approval-gate", current.VersionID, "already on version")
	if status := appStatus(t, cli, "approval-gate"); servingVersion(status) != current.VersionID || status.Versions != 2 {
		t.Errorf("after the refused rollback: serving %d of %d versions, want the second of two", servingVersion(status), status.Versions)
	}

	requireAppRollbackRefusal(t, cli, "never-installed", 0, "approval-gate", "standup-digest")
}

func requireAppRollbackRefusal(t *testing.T, cli *client.Client, name string, versionID int, want ...string) {
	t.Helper()
	rolled, err := cli.AppRollback(name, versionID)
	if err == nil {
		t.Fatalf("rollback of %s onto %d = %+v, want a refusal", name, versionID, rolled)
	}
	for _, text := range want {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("the rollback refusal does not say %q: %v", text, err)
		}
	}
}

func appManifestDeclaration(t *testing.T, m appbuild.Manifest) string {
	t.Helper()
	m.AttnAppAPI = appbuild.APIVersion
	m.Entrypoint = "src/index.ts"
	declaration, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	return declaration
}

func appTileView(name, title string) appbuild.View {
	return appbuild.View{Name: name, Kind: appbuild.ViewKindTile, Title: title, Entrypoint: "src/views/" + name + ".tsx"}
}

func stageAppVersion(t *testing.T, name, declaration, bundle string, views ...appbuild.ViewArtifact) string {
	t.Helper()
	hash := appbuild.VersionHash(declaration, []byte(bundle), views)
	writeAppArtifact(t, appbuild.ArtifactPath(config.AppsDir(), name, hash), []byte(bundle))
	for _, v := range views {
		writeAppArtifact(t, appbuild.ViewArtifactPath(config.AppsDir(), name, hash, v.Name), v.Content)
	}
	return hash
}

func applyAppVersion(t *testing.T, cli *client.Client, name, declaration, bundle string, views ...appbuild.ViewArtifact) *protocol.AppApplyResult {
	t.Helper()
	result, err := cli.AppApply(name, stageAppVersion(t, name, declaration, bundle, views...), declaration, "")
	if err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
	return result
}

func writeAppArtifact(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
