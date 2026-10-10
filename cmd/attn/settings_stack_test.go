package main_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSettingsCLIEnforcesScopesAndExplainsItsKeys(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	root := filepath.Join(s.Dir, "notes")
	requireStdout(t, s.Attn("settings", "set", "notebook.root", root), "notebook.root = "+root)
	requireStdout(t, s.Attn("settings", "get", "notebook.root"), root)
	unset := s.Attn("settings", "get", "chief_context_window_cap")
	if unset.Code != 0 || strings.TrimSpace(unset.Stdout) != "" {
		t.Fatalf("unset setting: %+v", unset)
	}
	requireStdout(t, s.Attn("settings", "set", "headless_tasks.enabled", "true"), "headless_tasks.enabled = true")
	requireStdout(t, s.Attn("settings", "get", "headless_tasks.enabled"), "true")
	id := uuid.NewString()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: id, Name: "Work"}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == id })
	if !created.Success {
		t.Fatal(protocol.Deref(created.Error))
	}
	work := created.Profile
	id = uuid.NewString()
	selected := testworld.Request(app, protocol.ProfileSelectMessage{Cmd: protocol.CmdProfileSelect, RequestID: id, ProfileID: work.ID}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == id })
	if !selected.Success {
		t.Fatal(protocol.Deref(selected.Error))
	}
	session := s.Spawn(app, fakeagent.Claude, s.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.ProfileID = work.ID })
	workRoot := filepath.Join(s.Dir, "work-notes")
	inside := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Args: append([]string{"settings"}, args...), Session: session})
	}
	requireStdout(t, inside("set", "notebook.root", workRoot), workRoot)
	requireStdout(t, s.Attn("settings", "get", "notebook.root", "--profile", "Work"), workRoot)
	requireStdout(t, s.Attn("settings", "get", "notebook.root", "--profile", "Default"), root)
	for _, r := range []testworld.Result{inside("set", "notebook.root", root, "--profile", "Default"), inside("set", "pty_shared_host_enabled", "false", "--profile", "Default"), s.Attn("settings", "get", "notebook.root")} {
		if r.Code != 1 || !strings.Contains(r.Stderr, "--profile") {
			t.Fatalf("scope refusal: %+v", r)
		}
	}
	requireStdout(t, s.Attn("settings", "get", "theme"))
	if r := s.Attn("settings", "set", "theme", "dark", "--profile", "Bogus"); r.Code != 1 || !strings.Contains(r.Stderr, "Bogus") {
		t.Fatalf("unknown profile: %+v", r)
	}
	plain := s.Attn("settings", "list", "--profile", "Work")
	requireStdout(t, plain, "this profile", "all profiles", "Folder for this profile")
	if strings.Contains(plain.Stdout, "notebook.root.effective") {
		t.Fatal("list includes read-only values")
	}
	requireStdout(t, s.Attn("settings", "list", "--all", "--profile", "Work"), "notebook.root.effective")
	if r := s.Attn("settings", "get", "unknown.key", "--profile", "Work"); r.Code != 1 || !strings.Contains(r.Stderr, "unknown.key") {
		t.Fatalf("unknown key: %+v", r)
	}
	if r := inside("set", "default_model_<harness>", "some-model"); r.Code != 1 || !strings.Contains(r.Stderr, "concrete key") {
		t.Fatalf("family placeholder: %+v", r)
	}
	if r := s.Attn("settings", "set", "theme"); r.Code != 2 {
		t.Fatalf("usage: %+v", r)
	}
	prompttest.Equal(t, "settings-help", map[string]string{"help": s.Attn("settings", "--help").Stdout})
	var listed protocol.SettingsListResult
	s.Attn("settings", "list", "--all", "--json", "--profile", "Work").JSON(t, &listed)
	var registry strings.Builder
	for _, entry := range listed.Entries {
		if !protocol.Deref(entry.Family) && strings.Contains(entry.Key, "_cap_") {
			continue
		}
		if entry.Key == "claude_available" || entry.Key == "codex_available" || entry.Key == "copilot_available" || entry.Key == "claude_executable" || entry.Key == "codex_executable" || entry.Key == "copilot_executable" {
			continue
		}
		fmt.Fprintf(&registry, "%s\t%s\t%t\t%s\n", entry.Key, entry.Scope, protocol.Deref(entry.ReadOnly), entry.Description)
	}
	prompttest.Equal(t, "settings-registry", map[string]string{"registry": registry.String()})
}
