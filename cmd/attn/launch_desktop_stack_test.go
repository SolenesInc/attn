package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestLaunchDesktopsAreInstallSettingsVisibleThroughTheCLI(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "alder")
	s.Start()
	requireStdout(t, s.Attn("crew", "set", "alder", "--launch-desktop", "own"), "launch desktop: alder (no ⌘ number)", "profile:")
	member := crewRoster(t, s)["alder"]
	if member.LaunchDesktop == nil || member.LaunchDesktop.DesktopID == nil || protocol.Deref(member.ProfileName) == "" {
		t.Fatalf("crew list = %+v", member)
	}
	requireStdout(t, s.Attn("crew", "list"), "alder (no ⌘ number)", *member.ProfileName)
	charter, err := os.ReadFile(filepath.Join(s.Dir, "crew", "alder", "CHARTER.md"))
	if err != nil || string(charter) != "# alder\n" {
		t.Fatalf("launch setting changed charter: %q %v", charter, err)
	}
	dir := s.Path("check")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := s.Path("check.yaml")
	yaml := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Local check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", dir)
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	requireStdout(t, s.Attn("automation", "apply", "--file", file, "--launch-desktop", "own"), "Local check (no ⌘ number)")
	requireStdout(t, s.Attn("automation", "set", "1", "--launch-desktop", "own", "--desktop-name", "Review"), "Review (no ⌘ number)")
	assert := func() {
		t.Helper()
		var definitions []protocol.AutomationDefinitionSummary
		s.Attn("automation", "list").JSON(t, &definitions)
		if len(definitions) != 1 || definitions[0].LaunchDesktop == nil || protocol.Deref(definitions[0].LaunchDesktop.Label) != "Review (no ⌘ number)" || protocol.Deref(definitions[0].ProfileName) != *member.ProfileName {
			t.Fatalf("automation list = %+v", definitions)
		}
		shown := s.Attn("automation", "show", "1")
		requireStdout(t, shown, "# Profile: "+*member.ProfileName, "# Launch desktop: Review (no ⌘ number)")
		if strings.Contains(shown.Stdout, "launch_desktop:") {
			t.Fatal("install setting leaked into definition YAML")
		}
	}
	rejected := s.Attn("automation", "set", "1", "--launch-desktop", "1", "--desktop-name", "Ignored")
	if rejected.Code == 0 || !strings.Contains(rejected.Stderr, "--desktop-name needs --launch-desktop own") {
		t.Fatalf("existing desktop accepted ignored name: %+v", rejected)
	}
	assert()
	s.Stop()
	s.Start()
	assert()
}
