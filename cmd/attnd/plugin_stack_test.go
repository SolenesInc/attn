package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type pluginResult struct {
	OK         bool   `json:"ok"`
	Name       string `json:"name"`
	LinkTarget string `json:"link_target"`
	Restart    bool   `json:"restart_required"`
	Plugin     *struct {
		Name string `json:"name"`
	} `json:"plugin"`
}

type pluginListing struct {
	Plugins []struct {
		Name       string  `json:"name"`
		LinkTarget *string `json:"link_target"`
	} `json:"plugins"`
}

func writePluginSource(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "run"), []byte("#!/bin/sh\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("name = %q\nversion = \"0.1.0\"\nattn_api_version = %d\n\n[plugin]\nkind = \"executable\"\npath = \"bin/run\"\n", name, plugins.APIVersion)
	if err := os.WriteFile(filepath.Join(dir, plugins.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fmt.Sprintf("{\"name\": %q, \"private\": true}\n", name)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installedPlugins(t *testing.T, s *testworld.Stack) map[string]*string {
	t.Helper()
	var listing pluginListing
	s.Attn("plugin", "list").JSON(t, &listing)
	installed := map[string]*string{}
	for _, p := range listing.Plugins {
		installed[p.Name] = p.LinkTarget
	}
	return installed
}

func TestPluginCommandsInstallLinkAndRemoveThroughTheRunningDaemon(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	copied, checkout := s.Path("copied-source"), s.Path("checkout")
	writePluginSource(t, copied, "copied")
	writePluginSource(t, checkout, "linked")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"plugin", "install"}, want: "usage: attn plugin install <source>"},
		{args: []string{"plugin", "install", copied, "--path", copied}, want: "usage: attn plugin install <source>"},
		{args: []string{"plugin", "link"}, want: "usage: attn plugin link <dir>"},
		{args: []string{"plugin", "reload"}, want: "usage: attn plugin reload <name> [--no-install]"},
		{args: []string{"plugin", "update"}, want: "usage: attn plugin update <name>"},
		{args: []string{"plugin", "remove"}, want: "usage: attn plugin remove <name>"},
	} {
		requireFailure(t, s.Attn(tc.args...), tc.want)
	}

	s.Start()
	var installed pluginResult
	s.Attn("plugin", "install", "--path", copied).JSON(t, &installed)
	if !installed.OK || installed.Name != "copied" || installed.Plugin == nil || installed.Plugin.Name != "copied" || installed.Restart || installed.LinkTarget != "" {
		t.Errorf("plugin install printed %+v, want copied installed with no restart", installed)
	}
	var linked pluginResult
	s.Attn("plugin", "link", checkout).JSON(t, &linked)
	if !linked.OK || linked.Name != "linked" || linked.LinkTarget != checkout || linked.Restart {
		t.Errorf("plugin link printed %+v, want linked pointing at %s", linked, checkout)
	}
	listed := installedPlugins(t, s)
	if target, ok := listed["copied"]; !ok || target != nil {
		t.Errorf("the daemon lists %v, want copied installed as a copy", listed)
	}
	if target, ok := listed["linked"]; !ok || target == nil || *target != checkout {
		t.Errorf("the daemon lists %v, want linked pointing at %s", listed, checkout)
	}

	requireFailure(t, s.Attn("plugin", "remove", "missing"), "plugin remove: ", `plugin "missing" is not installed`)
	var removed pluginResult
	s.Attn("plugin", "remove", "copied").JSON(t, &removed)
	if !removed.OK || removed.Name != "copied" {
		t.Errorf("plugin remove printed %+v", removed)
	}
	if listed := installedPlugins(t, s); len(listed) != 1 || listed["linked"] == nil {
		t.Errorf("after removing copied the daemon lists %v, want linked alone", listed)
	}
}

type pluginListingEntry struct {
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	Connected  bool    `json:"connected"`
	LinkTarget *string `json:"link_target"`
	Source     *string `json:"source"`
	SourceKind *string `json:"source_kind"`
}

func listedPlugin(t *testing.T, s *testworld.Stack, name string) pluginListingEntry {
	t.Helper()
	var listing struct {
		Plugins []pluginListingEntry `json:"plugins"`
	}
	s.Attn("plugin", "list").JSON(t, &listing)
	for _, p := range listing.Plugins {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("attn plugin list has no %s: %+v", name, listing.Plugins)
	return pluginListingEntry{}
}

func setPluginVersion(t *testing.T, dir, version string) {
	t.Helper()
	path := filepath.Join(dir, plugins.ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := regexp.MustCompile(`(?m)^version = ".*"$`).ReplaceAllString(string(data), fmt.Sprintf("version = %q", version))
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginCommandsReplaceReloadAndUpdateAHarnessWhileItsSessionTakesInput(t *testing.T) {
	t.Parallel()
	harnessID := "live_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	plugin := authorsPlugin(t, harnessID, packSDK(t))
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	s.Attn("plugin", "install", plugin).JSON(t, &pluginResult{})
	if p := listedPlugin(t, s, "sample-harness"); p.Version != "0.1.0" || protocol.Deref(p.Source) != plugin || protocol.Deref(p.SourceKind) != "path" {
		t.Fatalf("after installing %s the daemon lists %+v, want 0.1.0 from that directory", plugin, p)
	}
	agent := fakeagent.Harness(harnessID)
	awaitAgentAvailable(app, agent)
	session := s.Spawn(app, agent, s.Path("shop"))
	app.AwaitScreen(session, "sample harness started conversation")

	setPluginVersion(t, plugin, "0.2.0")
	var updated pluginResult
	s.Attn("plugin", "update", "sample-harness").JSON(t, &updated)
	if p := listedPlugin(t, s, "sample-harness"); !updated.OK || p.Version != "0.2.0" || !p.Connected {
		t.Fatalf("after update the daemon lists %+v, want 0.2.0 connected", p)
	}
	if got := submitDirectInput(app, session, "after the update"); !got.Success {
		t.Fatalf("after the update direct input was refused: %+v", got)
	}
	app.AwaitScreen(session, "[user] after the update")

	var linked pluginResult
	s.Attn("plugin", "link", "--path", plugin).JSON(t, &linked)
	if p := listedPlugin(t, s, "sample-harness"); linked.LinkTarget != plugin || protocol.Deref(p.LinkTarget) != plugin || p.Source != nil {
		t.Fatalf("after linking the daemon lists %+v, want it linked to %s", p, plugin)
	}
	setPluginVersion(t, plugin, "0.3.0")
	s.Attn("plugin", "reload", "sample-harness").JSON(t, &pluginResult{})
	setPluginVersion(t, plugin, "0.4.0")
	s.Attn("plugin", "reload", "sample-harness", "--no-install").JSON(t, &pluginResult{})
	if p := listedPlugin(t, s, "sample-harness"); p.Version != "0.4.0" || !p.Connected {
		t.Fatalf("after reloading the daemon lists %+v, want 0.4.0 connected", p)
	}
	requireFailure(t, s.Attn("plugin", "update", "sample-harness"), "plugin update: ", "plugin \"sample-harness\" is linked to "+plugin+"; use reload to pick up its changes")
}
