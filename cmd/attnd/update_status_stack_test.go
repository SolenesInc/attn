package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/testworld"
)

type releaseSource struct {
	url     string
	lookups atomic.Int32
}

func serveLatestRelease(t *testing.T, version string) *releaseSource {
	t.Helper()
	source := &releaseSource{}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		source.lookups.Add(1)
		http.Redirect(w, r, "/releases/tag/v"+version, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, r *http.Request) {})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	source.url = server.URL + "/releases/download"
	return source
}

func awaitSetting(app *testworld.Peer, key string) any {
	if value := app.Initial.Settings[key]; value != nil && value != "" {
		return value
	}
	update := testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings[key] != nil && m.Settings[key] != ""
	})
	return update.Settings[key]
}

func updateStatus(t *testing.T, stack *testworld.Stack) protocol.UpdateStatus {
	t.Helper()
	if result := stack.Attn("settings", "set", setting.SelfUpdateEnabled.Key, "true"); result.Code != 0 {
		t.Fatalf("turn on self-update: %+v", result)
	}
	result := stack.Attn("update", "status", "--json")
	if result.Code != 0 {
		t.Fatalf("attn update status: %+v", result)
	}
	t.Log(result.Stdout)
	var status protocol.UpdateStatus
	result.JSON(t, &status)
	return status
}

func inMacAppBundle(t *testing.T, binary string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "attn.app", "Contents", "MacOS")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"attn", "attnd"} {
		bytes, err := os.ReadFile(filepath.Join(filepath.Dir(binary), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), bytes, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "attn")
}

func TestUpdateStatusIsOffUntilTheFeatureIsOn(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithoutAgentKit())
	stack.Start()
	result := stack.Attn("update", "status")
	if result.Code == 0 || !strings.Contains(result.Stderr, setting.SelfUpdateEnabled.Key) {
		t.Fatalf("attn update status with the feature off: %+v", result)
	}
}

func TestUpdateStatusNamesEachInstallKind(t *testing.T) {
	release := testworld.AttnUpdateBinary(t, "1.0.0", "release", serveLatestRelease(t, "1.0.0").url)
	source := testworld.AttnUpdateBinary(t, "1.0.0", "source", "")
	unknown := testworld.AttnUpdateBinary(t, "1.0.0", "unknown", "")
	for _, install := range []struct {
		kind, binary, instance string
	}{
		{"release_app", inMacAppBundle(t, release), ""},
		{"release_binary", release, ""},
		{"source", source, ""},
		{"named_instance", source, "updatetest"},
		{"unknown", unknown, ""},
	} {
		t.Run(install.kind, func(t *testing.T) {
			stack := testworld.NewStack(t, testworld.WithoutAgentKit())
			stack.RunFrom(install.binary)
			if install.instance != "" {
				stack.Vars = append(stack.Vars, "ATTN_INSTANCE="+install.instance)
			}
			stack.Start()
			status := updateStatus(t, stack)
			if status.Kind != install.kind || status.Version != "1.0.0" {
				t.Fatalf("status = %+v, want kind %s at 1.0.0", status, install.kind)
			}
			if install.kind == "unknown" {
				refusal := protocol.Deref(status.Refusal)
				if !strings.Contains(refusal, `"unknown"`) || !strings.Contains(refusal, "make install") {
					t.Fatalf("refusal = %q, want the channel and make install", refusal)
				}
			}
		})
	}
}

func TestUpdateStatusOnAnOutpostAnswersFromTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	if result := home.Attn("settings", "set", setting.SelfUpdateEnabled.Key, "true"); result.Code != 0 {
		t.Fatalf("turn on self-update: %+v", result)
	}
	result := outpost.Attn("update", "status", "--json")
	if result.Code != 0 {
		t.Fatalf("attn update status on the outpost: %+v", result)
	}
	t.Log(result.Stdout)
	var status protocol.UpdateStatus
	result.JSON(t, &status)
	if status.Kind != "outpost" || protocol.Deref(status.Outpost) != "outpost" || protocol.Deref(status.HomeKind) != "unknown" {
		t.Fatalf("status from the outpost = %+v", status)
	}
}

func TestAPublishedReleaseReachesTheCLIAndTheApp(t *testing.T) {
	source := serveLatestRelease(t, "99.0.0")
	stack := testworld.NewStack(t, testworld.WithoutAgentKit())
	stack.RunFrom(testworld.AttnUpdateBinary(t, "1.0.0", "release", source.url))
	stack.Start()
	app := stack.App()
	if version := awaitSetting(app, setting.UpdateAvailableVersion.Key); version != "99.0.0" {
		t.Fatalf("the app's available version = %q", version)
	}
	status := updateStatus(t, stack)
	if protocol.Deref(status.AvailableVersion) != "99.0.0" || !strings.HasSuffix(protocol.Deref(status.AvailableURL), "/releases/tag/v99.0.0") {
		t.Fatalf("status = %+v", status)
	}
	if source.lookups.Load() == 0 {
		t.Fatal("the daemon never asked its release source")
	}
}

func TestASourceInstallNeverAsksForReleases(t *testing.T) {
	source := serveLatestRelease(t, "99.0.0")
	stack := testworld.NewStack(t, testworld.WithoutAgentKit())
	stack.RunFrom(testworld.AttnUpdateBinary(t, "1.0.0", "source", source.url))
	stack.Start()
	status := updateStatus(t, stack)
	if status.AvailableVersion != nil || status.CheckedAt != nil || source.lookups.Load() != 0 {
		t.Fatalf("a source install looked for releases: %+v, lookups %d", status, source.lookups.Load())
	}
	if status.Commit == "" {
		t.Fatalf("status names no installed commit: %+v", status)
	}
}
