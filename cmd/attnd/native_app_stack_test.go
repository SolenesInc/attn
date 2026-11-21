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
	if found.Role != "button" {
		t.Fatalf("the sidebar row for %s is a %q, want a button", session, found.Role)
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

func TestTheNativeAppAllowsAndDeniesAProgramThatAsksToPair(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.StartNativeApp()
	dialog := map[string]string{"key": "sender-pairing-dialog"}

	allowed := s.Launch(testworld.Invocation{Args: []string{"send", "pair", "--name", "deploy-bot", "--json"}})
	allowed.AwaitStdout(`"code"`)
	stdout, _ := allowed.Output()
	var asked protocol.SenderPairingRequest
	if err := json.Unmarshal([]byte(strings.SplitN(stdout, "\n", 2)[0]), &asked); err != nil {
		t.Fatalf("send pair first line %q: %v", stdout, err)
	}
	var shown struct {
		Name string `json:"name"`
	}
	app.Must("wait", map[string]any{"target": dialog}, &shown)
	if shown.Name != "Allow “deploy-bot” to send messages as you?" {
		t.Fatalf("the dialog asks %q", shown.Name)
	}
	var code struct {
		Name string `json:"name"`
	}
	app.Must("find", map[string]any{"target": map[string]string{"key": "sender-pairing.match.code"}}, &code)
	if code.Name != asked.Code {
		t.Fatalf("the dialog shows code %q; the program printed %q", code.Name, asked.Code)
	}
	app.Must("press", map[string]any{"target": map[string]string{"key": "sender-pairing.allow"}}, nil)
	result := allowed.Wait()
	if result.Code != 0 {
		t.Fatalf("send pair after Allow: %+v", result)
	}
	var paired protocol.PairedSenderToken
	if err := json.Unmarshal([]byte(strings.SplitN(strings.TrimSpace(result.Stdout), "\n", 2)[1]), &paired); err != nil || paired.Token == "" || paired.Sender.Name != "deploy-bot" {
		t.Fatalf("send pair second line %q: %v", result.Stdout, err)
	}
	app.Must("wait", map[string]any{"target": dialog, "gone": true}, nil)

	denied := s.Launch(testworld.Invocation{Args: []string{"send", "pair", "--name", "backup-script", "--json"}})
	denied.AwaitStdout(`"code"`)
	app.Must("wait", map[string]any{"target": dialog}, &shown)
	if shown.Name != "Allow “backup-script” to send messages as you?" {
		t.Fatalf("the dialog asks %q", shown.Name)
	}
	app.Must("press", map[string]any{"target": map[string]string{"key": "sender-pairing.deny"}}, nil)
	result = denied.Wait()
	if result.Code == 0 || !strings.Contains(result.Stderr, `the user denied pairing "backup-script"`) {
		t.Fatalf("send pair after Deny: %+v", result)
	}
	app.Must("wait", map[string]any{"target": dialog, "gone": true}, nil)
}
