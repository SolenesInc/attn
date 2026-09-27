package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/plugins"
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
		Name              string  `json:"name"`
		InstallationState string  `json:"installation_state"`
		LinkTarget        *string `json:"link_target"`
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
		if p.InstallationState == "installed" {
			installed[p.Name] = p.LinkTarget
		}
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
		{args: []string{"plugin", "install"}, want: "plugin install: --path is required"},
		{args: []string{"plugin", "link"}, want: "plugin link: --path is required"},
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
	s.Attn("plugin", "link", "--path", checkout).JSON(t, &linked)
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
