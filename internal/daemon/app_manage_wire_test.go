package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAppListShowsTheServingVersionAndWhetherItsConsumerRuns(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	if got := appNames(t, cli); got != "" {
		t.Fatalf("a fresh daemon lists %q", got)
	}
	applied := applyApp(t, cli, "approval-gate", "only")

	listed := listedApp(t, cli, "approval-gate")
	if listed.CurrentVersion == nil || listed.CurrentVersion.ID != applied.VersionID || listed.CurrentVersion.ContentHash != applied.ContentHash {
		t.Fatalf("listed version = %+v, want %d with hash %s", listed.CurrentVersion, applied.VersionID, applied.ContentHash)
	}
	if listed.Consumer == nil || listed.Consumer.Name != "app:approval-gate" || !listed.Consumer.Enabled {
		t.Fatalf("listed consumer = %+v, want app:approval-gate enabled", listed.Consumer)
	}

	for _, enabled := range []bool{false, true} {
		flipped, err := cli.AppSetEnabled("approval-gate", enabled)
		if err != nil {
			t.Fatalf("set enabled=%t: %v", enabled, err)
		}
		if flipped.Name != "approval-gate" || flipped.Consumer != "app:approval-gate" || flipped.Enabled != enabled {
			t.Errorf("set enabled=%t answered %+v", enabled, flipped)
		}
		if consumer := listedApp(t, cli, "approval-gate").Consumer; consumer == nil || consumer.Enabled != enabled {
			t.Errorf("after set enabled=%t the list shows consumer %+v", enabled, consumer)
		}
		if consumer := appStatus(t, cli, "approval-gate").App.Consumer; consumer == nil || consumer.Enabled != enabled {
			t.Errorf("after set enabled=%t the status shows consumer %+v", enabled, consumer)
		}
		if enabled {
			continue
		}
		parked := listedApp(t, cli, "approval-gate").Consumer
		defineRequests(t, cli, gateNS)
		put(t, cli, gateNS, "first", `{}`)
		last := put(t, cli, gateNS, "second", `{}`)
		if behind := listedApp(t, cli, "approval-gate").Consumer; behind.Cursor != parked.Cursor || behind.Lag != last.Seq-parked.Cursor {
			t.Errorf("after two writes up to seq %d the disabled consumer lists %+v, want cursor %d and lag %d", last.Seq, behind, parked.Cursor, last.Seq-parked.Cursor)
		}
	}
}

func TestAppRemoveKeepsHistoryAndDocuments(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	reviewer := applyAppWithViews(t, cli, "reviewer", "", appTileView("approvals", "Pending approvals"))
	app := w.App()
	reportAppViewCrash(app, reviewer.VersionID, "TypeError: kept as history")
	flushAppPeer(app)

	removed, err := cli.AppRemove("reviewer")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.Name != "reviewer" || !removed.ConsumerRemoved || removed.VersionsKept != 1 || removed.InvocationsKept != 1 || removed.NamespaceKept != "app/reviewer" {
		t.Errorf("remove answered %+v, want the consumer gone and one version, one invocation and app/reviewer kept", removed)
	}
	testworld.Await(app, protocol.EventAppsUpdated, func(m protocol.AppsUpdatedMessage) bool {
		for _, e := range m.Apps {
			if e.Name == "reviewer" {
				return false
			}
		}
		return true
	})
	if got := appNames(t, cli); got != "" {
		t.Errorf("apps after the removal = %q", got)
	}
	_, err = cli.AppStatus("reviewer")
	if err == nil || !strings.Contains(err.Error(), "1 version(s)") || !strings.Contains(err.Error(), "no apps are registered") {
		t.Errorf("status of the removed app = %v, want it to point at the surviving version and say nothing is registered", err)
	}
}

func TestAppCommandsOnAnUnknownOrInvalidNameSayWhatExists(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	applyAppVersion(t, cli, "approval-gate", appManifestDeclaration(t, appbuild.Manifest{Name: "approval-gate"}), "export default {}\n")

	for _, tc := range []struct {
		name string
		want []string
	}{
		{"never-installed", []string{"never-installed", "approval-gate"}},
		{"Approval Gate", []string{"lowercase"}},
	} {
		for verb, call := range map[string]func(string) error{
			"status": func(name string) error { _, err := cli.AppStatus(name); return err },
			"enable": func(name string) error { _, err := cli.AppSetEnabled(name, true); return err },
			"remove": func(name string) error { _, err := cli.AppRemove(name); return err },
		} {
			err := call(tc.name)
			if err == nil {
				t.Errorf("app %s %q succeeded", verb, tc.name)
				continue
			}
			for _, text := range tc.want {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("app %s %q refused with %q, want it to say %q", verb, tc.name, err, text)
				}
			}
		}
	}
}

func listedApp(t *testing.T, cli *client.Client, name string) protocol.AppSummary {
	t.Helper()
	list, err := cli.AppList()
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	for _, app := range list.Apps {
		if app.Name == name {
			return app
		}
	}
	t.Fatalf("app %s is not listed", name)
	return protocol.AppSummary{}
}
