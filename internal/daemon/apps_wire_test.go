package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func TestApplyingABundleServesItAndReapplyingItMintsNothing(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()

	first := applyApp(t, cli, "approval-gate", "first")
	second := applyApp(t, cli, "approval-gate", "second")
	if !first.VersionCreated || !second.VersionCreated || protocol.Deref(second.PreviousVersionID) != first.VersionID {
		t.Fatalf("applies = %+v then %+v, want two new versions, the second over the first", first, second)
	}
	status := appStatus(t, cli, "approval-gate")
	if servingVersion(status) != second.VersionID || status.Versions != 2 {
		t.Fatalf("serving %d of %d versions, want the second of two", servingVersion(status), status.Versions)
	}
	if got := versionIDs(status.RecentVersions); got != fmt.Sprint([]int{second.VersionID, first.VersionID}) {
		t.Errorf("versions = %s, want newest first", got)
	}

	again := applyApp(t, cli, "approval-gate", "second")
	if again.VersionCreated || again.VersionID != second.VersionID {
		t.Fatalf("re-applying identical content = %+v, want the existing version %d", again, second.VersionID)
	}
	if status := appStatus(t, cli, "approval-gate"); status.Versions != 2 || servingVersion(status) != second.VersionID {
		t.Fatalf("after the re-apply: serving %d of %d versions", servingVersion(status), status.Versions)
	}
	rolled, err := cli.AppRollback("approval-gate", 0)
	if err != nil || rolled.VersionID != first.VersionID {
		t.Fatalf("a bare rollback after the no-op re-apply = %+v, %v; want the first version", rolled, err)
	}
}

func TestNamedRollbackSwitchesServingAndKeepsHistory(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()
	older := applyApp(t, cli, "approval-gate", "older")
	newer := applyApp(t, cli, "approval-gate", "newer")
	other := applyApp(t, cli, "standup-digest", "only")

	if _, err := cli.AppRollback("approval-gate", older.VersionID); err != nil {
		t.Fatalf("roll back onto the older version: %v", err)
	}
	status := appStatus(t, cli, "approval-gate")
	if servingVersion(status) != older.VersionID || status.Versions != 2 {
		t.Fatalf("serving %d of %d versions, want the older of two", servingVersion(status), status.Versions)
	}
	if _, err := cli.AppRollback("approval-gate", other.VersionID); err == nil {
		t.Error("a rollback onto another app's version was accepted")
	}
	if _, err := cli.AppRollback("approval-gate", newer.VersionID+9999); err == nil {
		t.Error("a rollback onto a version that does not exist was accepted")
	}
	back, err := cli.AppRollback("approval-gate", 0)
	if err != nil || back.VersionID != newer.VersionID {
		t.Errorf("a bare rollback after the named one = %+v, %v; want the version served before it", back, err)
	}
}

func TestBareRollbackWalksDownTheServingHistory(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()
	v1 := applyApp(t, cli, "approval-gate", "v1")
	v2 := applyApp(t, cli, "approval-gate", "v2")
	v3 := applyApp(t, cli, "approval-gate", "v3")

	if got := versionIDs(appStatus(t, cli, "approval-gate").ServingHistory); got != fmt.Sprint([]int{v3.VersionID, v2.VersionID, v1.VersionID}) {
		t.Fatalf("serving history = %s, want v3 v2 v1", got)
	}
	bareRollback(t, cli, v2.VersionID)
	bareRollback(t, cli, v1.VersionID)
	if _, err := cli.AppRollback("approval-gate", 0); err == nil {
		t.Fatal("a bare rollback past the oldest served version was accepted")
	}
	if status := appStatus(t, cli, "approval-gate"); servingVersion(status) != v1.VersionID || versionIDs(status.ServingHistory) != fmt.Sprint([]int{v1.VersionID}) {
		t.Fatalf("after the walk: serving %d with history %s", servingVersion(status), versionIDs(status.ServingHistory))
	}

	fix := applyApp(t, cli, "approval-gate", "v4")
	bareRollback(t, cli, v1.VersionID)
	if status := appStatus(t, cli, "approval-gate"); status.Versions != 4 {
		t.Errorf("the walk changed the versions to %d", status.Versions)
	}
	if _, err := cli.AppRollback("approval-gate", v3.VersionID); err != nil {
		t.Fatalf("naming a version the walk passed: %v", err)
	}
	if got := versionIDs(appStatus(t, cli, "approval-gate").ServingHistory); got != fmt.Sprint([]int{v3.VersionID, v1.VersionID}) {
		t.Errorf("serving history after naming v3 = %s, want v3 over v1 (not the unserved %d)", got, fix.VersionID)
	}
}

func TestAppServingHistoryIsCappedButCountsEveryStep(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()
	var applied []int
	for i := range 12 {
		applied = append(applied, applyApp(t, cli, "approval-gate", fmt.Sprintf("v%d", i)).VersionID)
	}
	status := appStatus(t, cli, "approval-gate")
	if len(status.ServingHistory) != 10 || status.ServingHistory[0].ID != applied[11] || status.ServingHistory[9].ID != applied[2] {
		t.Fatalf("serving history = %s, want the newest ten from the top", versionIDs(status.ServingHistory))
	}
	if steps := protocol.Deref(status.ServingHistorySteps); steps != 12 {
		t.Errorf("serving history counts %d steps, want all 12", steps)
	}
}

func TestAppListIsByNameAndRemovalDropsTheApp(t *testing.T) {
	w := newWorld(t)
	cli := w.cli()
	applyApp(t, cli, "standup-digest", "only")
	applyApp(t, cli, "approval-gate", "only")

	if got := appNames(t, cli); got != "approval-gate standup-digest" {
		t.Fatalf("apps = %q, want both by name", got)
	}
	if _, err := cli.AppStatus("never-installed"); err == nil || !strings.Contains(err.Error(), "no app named") {
		t.Errorf("the status of an unknown app = %v, want not found", err)
	}

	if _, err := cli.AppRemove("approval-gate"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := appNames(t, cli); got != "standup-digest" {
		t.Errorf("apps after the removal = %q", got)
	}
	if _, err := cli.AppRemove("approval-gate"); err == nil || !strings.Contains(err.Error(), "no app named") {
		t.Errorf("a second removal = %v, want not installed", err)
	}
}

func applyApp(t *testing.T, cli *client.Client, name, note string) *protocol.AppApplyResult {
	t.Helper()
	declaration := fmt.Sprintf(`{"name":%q,"attn_app_api":1,"entrypoint":"src/index.ts"}`, name)
	bundle := []byte("export default {} // " + note)
	hash := appbuild.VersionHash(declaration, bundle, nil)
	path := appbuild.ArtifactPath(config.AppsDir(), name, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := cli.AppApply(name, hash, declaration, "")
	if err != nil {
		t.Fatalf("apply %s (%s): %v", name, note, err)
	}
	return result
}

func appStatus(t *testing.T, cli *client.Client, name string) *protocol.AppStatusResult {
	t.Helper()
	status, err := cli.AppStatus(name)
	if err != nil {
		t.Fatalf("status of %s: %v", name, err)
	}
	return status
}

func bareRollback(t *testing.T, cli *client.Client, want int) {
	t.Helper()
	rolled, err := cli.AppRollback("approval-gate", 0)
	if err != nil || rolled.VersionID != want {
		t.Fatalf("bare rollback = %+v, %v; want version %d", rolled, err, want)
	}
}

func servingVersion(status *protocol.AppStatusResult) int {
	if status.App.CurrentVersion == nil {
		return 0
	}
	return status.App.CurrentVersion.ID
}

func versionIDs(versions []protocol.AppVersionInfo) string {
	ids := make([]int, 0, len(versions))
	for _, v := range versions {
		ids = append(ids, v.ID)
	}
	return fmt.Sprint(ids)
}

func appNames(t *testing.T, cli *client.Client) string {
	t.Helper()
	list, err := cli.AppList()
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	names := make([]string, 0, len(list.Apps))
	for _, app := range list.Apps {
		names = append(names, app.Name)
	}
	return strings.Join(names, " ")
}
