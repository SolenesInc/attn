package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAClaudeCrewLaunchTrustsItsHomeWithoutChangingOtherConfig(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	configPath := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
	before := `{"oauthAccount":{"uuid":"keep-me"},"future":9007199254740993,"projects":{"/other":{"hasTrustDialogAccepted":false,"future":{"keep":true}}}}`
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel", Agent: protocol.Ptr("claude")})
	if err != nil {
		t.Fatal(err)
	}
	beforeWake, err := os.ReadFile(configPath)
	if err != nil || string(beforeWake) != before {
		t.Fatalf("create changed Claude config: %s (%v)", beforeWake, err)
	}
	w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
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

}

func TestAClaudeCrewWakeRetriesAConfigWriteWhileAnotherWriterFinishes(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"}); err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json.lock")
		if err := os.MkdirAll(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() {
			_, err := cli.CrewWake("Keel", "", "")
			finished <- err
		}()
		synctest.Wait()
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
		if err := <-finished; err != nil {
			t.Fatalf("wake failed instead of retrying the writer: %v", err)
		}
	})
}

func TestACodexCrewWakeReceivesTheHomeTrustOverride(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel", Agent: protocol.Ptr("codex")})
	if err != nil {
		t.Fatal(err)
	}
	day := w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
	trusted := `projects.` + strconv.Quote(created.Member.HomeDir) + `.trust_level="trusted"`
	if !strings.Contains(strings.Join(day.Argv, " "), trusted) {
		t.Fatalf("Codex home trust missing from launch: %q", day.Argv)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("Codex wake wrote Claude's config: %v", err)
	}
}

func TestCreatingMembersDoesNotWriteHarnessTrustConfig(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot} {
		t.Run(string(harness), func(t *testing.T) {
			w := newWorld(t, harness)
			path := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			before := []byte(`{"keep":"this"}`)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Client().CrewCreate(protocol.CrewCreateMessage{Name: "Keel", Agent: protocol.Ptr(string(harness))}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != string(before) {
				t.Fatalf("create changed trust config: %s (%v)", raw, err)
			}
		})
	}
}

func TestAnAlreadyTrustedClaudeHomeWakesWithoutTakingTheConfigLock(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	member := crewRosterMember(t, cli, "keel")
	path := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
	if err := os.MkdirAll(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(map[string]any{"projects": map[string]any{member.HomeDir: map[string]any{"hasTrustDialogAccepted": true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(wakeCrew(t, cli, "keel", "").SessionID))
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(info, after) {
		t.Fatalf("trusted config was replaced: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("another writer's lock was removed: %v", err)
	}
}

func TestClaudeHomeTrustPreservesASymlinkedConfigsModeAndOtherFields(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	member := crewRosterMember(t, cli, "keel")
	path := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
	target := filepath.Join(w.Dir, "claude-user-config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"keep":"this"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	w.Launched(string(wakeCrew(t, cli, "keel", "").SessionID))
	if dest, err := os.Readlink(path); err != nil || dest != target {
		t.Fatalf("config symlink changed: %s (%v)", dest, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions changed: %v (%v)", info, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Keep     string `json:"keep"`
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.Keep != "this" || !config.Projects[member.HomeDir].Trusted {
		t.Fatalf("symlink target not preserved and trusted: %s", raw)
	}
}

func TestCrewWakeRecoversAStaleClaudeConfigLockAndRefusesAnActiveOne(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(strconv.FormatBool(stale), func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				_, err := w.Client().CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")
				lock := path + ".lock"
				if err := os.MkdirAll(lock, 0o700); err != nil {
					t.Fatal(err)
				}
				before := []byte(`{"keep":"this"}`)
				if err := os.WriteFile(path, before, 0o600); err != nil {
					t.Fatal(err)
				}
				if stale {
					abandoned := time.Now().Add(-time.Hour)
					if err := os.Chtimes(lock, abandoned, abandoned); err != nil {
						t.Fatal(err)
					}
				}
				_, err = w.Client().CrewWake("Keel", "", "")
				if stale {
					if err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(lock); !os.IsNotExist(err) {
						t.Fatalf("recovered lock was not released: %v", err)
					}
				} else {
					crewErrorContains(t, err, "config lock", "in use", "try again")
					raw, err := os.ReadFile(path)
					if err != nil || string(raw) != string(before) {
						t.Fatalf("active lock did not protect config: %s (%v)", raw, err)
					}
					if _, err := os.Stat(lock); err != nil {
						t.Fatalf("active lock was removed: %v", err)
					}
				}
			})
		})
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
			if _, err := os.Stat(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")); !os.IsNotExist(err) {
				t.Fatalf("unrelated cwd wrote Claude config: %v", err)
			}

		})
	}
}

func TestACrewHomeUsesTheClaudeConfigFromTheLoginShell(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	configDir := filepath.Join(w.Dir, "shell-claude-config")
	shell := filepath.Join(w.Dir, "login-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nif [ \"$3\" = 'env -0' ]; then printf 'CLAUDE_CONFIG_DIR="+configDir+"\\0'; exit 0; fi\nexport CLAUDE_CONFIG_DIR='"+configDir+"'\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("ATTN_CACHED_SHELL_ENV", "")
	w.start()
	cli := w.Client()
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	day := w.Launched(string(wakeCrew(t, cli, "Keel", "").SessionID))
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
	if !strings.Contains(strings.Join(day.Env, "\n"), "CLAUDE_CONFIG_DIR="+configDir) {
		t.Fatalf("Claude did not inherit the trusted config directory")
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "toolhome", ".claude", ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon config was written instead of login-shell config: %v", err)
	}
}

func TestCrewWakeUsesClaudesExistingLegacyConfig(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	root := filepath.Join(w.Dir, "toolhome", ".claude")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".config.json")
	if err := os.WriteFile(path, []byte(`{"keep":"this"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := w.Client().CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(wakeCrew(t, w.Client(), "Keel", "").SessionID))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Keep     string `json:"keep"`
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.Keep != "this" || !config.Projects[created.Member.HomeDir].Trusted {
		t.Fatalf("legacy config not preserved and trusted: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("unused modern config was written: %v", err)
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
