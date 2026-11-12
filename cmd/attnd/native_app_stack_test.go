package main_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheNativeAppListsADaemonSessionAndTakesKeysOverAutomation(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	session := s.Spawn(s.App(), fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))

	app := s.StartNativeApp()
	row := map[string]string{"key": "sidebar.session." + session}
	var found struct {
		Role string `json:"role"`
	}
	app.Must("wait", map[string]any{"target": row}, &found)
	if found.Role != "listitem" {
		t.Fatalf("the sidebar row for %s is a %q, want a listitem", session, found.Role)
	}
	var tree json.RawMessage
	app.Must("tree", nil, &tree)
	if !strings.Contains(string(tree), `"name":"Sidebar"`) {
		t.Fatalf("the accessibility tree has no Sidebar:\n%s", tree)
	}

	toggleSidebar := "mod+b"
	if runtime.GOOS == "linux" {
		toggleSidebar = "mod+alt+b"
	}
	app.Must("key", map[string]string{"chord": toggleSidebar}, nil)
	app.Must("wait", map[string]any{"target": map[string]string{"key": "sidebar"}, "gone": true}, nil)
	var window struct {
		SidebarHidden bool `json:"sidebar_hidden"`
	}
	app.Must("state", map[string]string{"of": "window"}, &window)
	if !window.SidebarHidden {
		t.Fatalf("%s left the sidebar showing", toggleSidebar)
	}

	app.Must("run", map[string]string{"name": "session.toggleSidebar"}, nil)
	app.Must("wait", map[string]any{"target": row}, nil)
}
