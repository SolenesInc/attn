package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentsUseTheChosenLaunchModeOnBothPTYBackends(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify direct children on the shared host")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if app.Initial.Settings["direct_agent_launch_enabled"] != "false" {
				t.Fatal("a fresh daemon must expose the direct-launch experiment as false")
			}
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			for _, direct := range []bool{false, true} {
				if direct {
					setSetting(t, app, "direct_agent_launch_enabled", "true")
				}
				for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
					session := s.Spawn(app, h, s.Path(string(h)), func(m *protocol.SpawnSessionMessage) {
						m.InitialPrompt = protocol.Ptr("literal 'quote' \\\\path\n$HOME and λ")
					})
					run := s.Launched(session)
					if got := run.Prompted(); got != "literal 'quote' \\\\path\n$HOME and λ" {
						t.Fatalf("%s received prompt %q", h, got)
					}
					pid := strconv.Itoa(run.PID)
					out, err := exec.Command("ps", "-p", pid, "-o", "ppid=").Output()
					if err != nil {
						t.Fatalf("read %s's parent: %v", h, err)
					}
					parent := strings.TrimSpace(string(out))
					out, err = exec.Command("ps", "-p", parent, "-o", "args=").Output()
					if err != nil {
						t.Fatalf("read parent %s: %v", parent, err)
					}
					want := " -s "
					if direct {
						want = " pty-worker "
					}
					if direct && backend == "shared host" {
						want = "attn-pty-host"
					}
					if !strings.Contains(string(out), want) {
						t.Fatalf("%s's parent is %s, want %s", h, out, want)
					}
					receipt, err := exec.Command("ps", "-p", fmt.Sprintf("%s,%s", pid, parent), "-o", "pid,ppid,rss,args").Output()
					if err != nil {
						t.Fatal(err)
					}
					receipt = regexp.MustCompile(`(--control-token )\S+`).ReplaceAll(receipt, []byte("${1}<redacted>"))
					t.Logf("%s %s process receipt:\n%s", backend, h, receipt)
				}
			}

		})
	}
}

func TestTheLaunchExperimentChangesNewAndReloadedAgentsAndSurvivesRestart(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {

			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify shared-host toggles")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			original := s.Launched(session)
			for _, value := range []string{"true", "false", "true"} {
				setSetting(t, app, "direct_agent_launch_enabled", value)
				app.TypeLine(session, "still the original process")
				if prompt := original.Prompted(); prompt != "still the original process" {
					t.Fatalf("toggle changed live process prompt %q", prompt)
				}
				original.Reply("same process <!-- attn:state=idle -->")
			}
			s.Stop()
			s.Start()
			app = s.App()
			if app.Initial.Settings["direct_agent_launch_enabled"] != "true" {
				t.Fatal("launch experiment did not survive daemon restart")
			}
			for _, direct := range []bool{true, false} {
				if !direct {
					setSetting(t, app, "direct_agent_launch_enabled", "false")
				}
				result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
				if !result.Success {
					t.Fatalf("reload: %s", protocol.Deref(result.Error))
				}
				run := s.Launched(session)
				out, err := exec.Command("ps", "-p", strconv.Itoa(run.PID), "-o", "ppid=").Output()
				if err != nil {
					t.Fatal(err)
				}
				parent, err := exec.Command("ps", "-p", strings.TrimSpace(string(out)), "-o", "args=").Output()
				if err != nil {
					t.Fatal(err)
				}
				want := " -s "
				if direct {
					want = " pty-worker "
					if backend == "shared host" {
						want = "attn-pty-host"
					}
				}
				if !strings.Contains(string(parent), want) {
					t.Fatalf("reloaded parent %s; want %s", parent, want)
				}
			}

		})
	}
}
