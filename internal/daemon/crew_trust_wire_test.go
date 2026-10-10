package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestACreatedCrewHomeIsTrustedWithoutChangingOtherClaudeConfig(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	configPath := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
	before := `{"oauthAccount":{"uuid":"keep-me"},"future":9007199254740993,"projects":{"/other":{"hasTrustDialogAccepted":false,"future":{"keep":true}}}}`
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel", Agent: protocol.Ptr("codex")})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(config["projects"], &projects); err != nil {
		t.Fatal(err)
	}
	if string(projects[created.Member.HomeDir]["hasTrustDialogAccepted"]) != "true" {
		t.Fatalf("created home was not trusted: %s", raw)
	}
	delete(projects, created.Member.HomeDir)
	config["projects"], _ = json.Marshal(projects)
	var wanted map[string]json.RawMessage
	_ = json.Unmarshal([]byte(before), &wanted)
	for key := range wanted {
		var gotValue, wantValue any
		decoder := json.NewDecoder(strings.NewReader(string(config[key])))
		decoder.UseNumber()
		_ = decoder.Decode(&gotValue)
		decoder = json.NewDecoder(strings.NewReader(string(wanted[key])))
		decoder.UseNumber()
		_ = decoder.Decode(&wantValue)
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("Claude config %s changed: got %s, want %s", key, config[key], wanted[key])
		}
	}
	wake := wakeCrew(t, cli, "Keel", "")
	day := w.Launched(string(wake.SessionID))
	trusted := `projects.` + strconv.Quote(created.Member.HomeDir) + `.trust_level="trusted"`
	if !strings.Contains(strings.Join(day.Argv, " "), trusted) {
		t.Fatalf("Codex launch did not trust its crew home: %q", day.Argv)
	}
}

func TestACrewLaunchDoesNotTrustAnUnrelatedWorkingDirectory(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			cwd := w.Path("project")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			cli := w.Client()
			created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel", Agent: protocol.Ptr(string(harness)), Cwd: protocol.Ptr(cwd)})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(created.Member.HomeDir); err != nil {
				t.Fatal(err)
			}
			day := w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
			if _, err := os.Stat(created.Member.HomeDir); err != nil {
				t.Fatalf("wake did not repair the missing home: %v", err)
			}
			if strings.Contains(strings.Join(day.Argv, " "), `.trust_level="trusted"`) {
				t.Fatalf("unrelated cwd trusted in argv: %q", day.Argv)
			}
			raw, err := os.ReadFile(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Projects map[string]json.RawMessage `json:"projects"`
			}
			if err := json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			if _, found := config.Projects[cwd]; found {
				t.Fatalf("unrelated cwd trusted in Claude config: %s", raw)
			}
		})
	}
}

func TestACrewHomeUsesTheClaudeConfigFromTheLoginShell(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	configDir := filepath.Join(w.Dir, "shell-claude-config")
	shell := filepath.Join(w.Dir, "login-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexport CLAUDE_CONFIG_DIR='"+configDir+"'\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	w.start()
	cli := w.Client()
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !config.Projects[created.Member.HomeDir].Trusted {
		t.Fatalf("home not trusted in login-shell config: %s", raw)
	}
	day := w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
	if !strings.Contains(strings.Join(day.Env, "\n"), "CLAUDE_CONFIG_DIR="+configDir) {
		t.Fatalf("Claude did not inherit the trusted config directory")
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon config was written instead of login-shell config: %v", err)
	}
}

func TestACrewWakeTrustsItsExistingClaudeHome(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	member := crewRosterMember(t, cli, "keel")
	path := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(map[string]any{"projects": map[string]any{member.HomeDir: map[string]any{"hasTrustDialogAccepted": false, "allowedTools": []string{"Read"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	w.Launched(string(wakeCrew(t, cli, "keel", "").SessionID))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Projects map[string]struct {
			Trusted bool     `json:"hasTrustDialogAccepted"`
			Tools   []string `json:"allowedTools"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if project := config.Projects[member.HomeDir]; !project.Trusted || !reflect.DeepEqual(project.Tools, []string{"Read"}) {
		t.Fatalf("wake did not preserve the trusted home: %s", raw)
	}
}
