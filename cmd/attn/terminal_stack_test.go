package main_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalCLI(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			s.Vars = append(s.Vars, "SHELL=/bin/bash")
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify terminal CLI on the shared host")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			caller := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			s.Launched(caller)
			run := func(args ...string) testworld.Result {
				return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
			}
			open := func() protocol.TerminalOpenResult {
				got := run("open", "--json", "--", "printf terminal-ready")
				if got.Code != 0 {
					t.Fatalf("open: %+v", got)
				}
				var opened protocol.TerminalOpenResult
				got.JSON(t, &opened)
				app.AwaitScreen(string(opened.SessionID), "terminal-ready")
				return opened
			}
			first := open()
			second := open()
			var entries []protocol.TerminalEntry
			listed := run("ls", "--json")
			listed.JSON(t, &entries)
			if listed.Code != 0 || len(entries) != 3 {
				t.Fatalf("ls: %+v %+v", listed, entries)
			}
			for _, entry := range entries {
				switch entry.SessionID {
				case first.SessionID:
					if entry.TerminalID != first.TerminalID || !strings.HasPrefix(string(entry.TerminalID), entry.ShortID) {
						t.Errorf("listed terminal identity: %+v", entry)
					}
					if protocol.Deref(entry.Position) != "right/top" || protocol.Deref(entry.OpenedBy) != protocol.SessionID(caller) {
						t.Errorf("first placement: %+v", entry)
					}
				case second.SessionID:
					if protocol.Deref(entry.Position) != "right/bottom" {
						t.Errorf("second placement: %+v", entry)
					}
				}
			}
			fresh := s.App()
			for _, desktop := range fresh.Initial.Desktops {
				if desktop.ID == first.DesktopID {
					for _, pane := range desktop.Panes {
						if string(pane.SessionID) == caller && desktop.ActivePaneID != pane.PaneID {
							t.Fatalf("open stole active tile: %+v", desktop)
						}
					}
				}
			}
			got := run("run", string(first.TerminalID), "printf 'command-output\\n'; (exit 7)", "--wait")
			if got.Code != 7 || strings.TrimSpace(got.Stdout) != "command-output" {
				t.Fatalf("run --wait: %+v", got)
			}
			last := run("read", string(first.TerminalID), "--last")
			if last.Code != 0 || strings.TrimSpace(last.Stdout) != "command-output" {
				t.Fatalf("read --last: %+v", last)
			}
			command := "for i in {1..60}; do printf 'history-%02d\\n' \"$i\"; done"
			if got := run("run", string(first.TerminalID), command, "--wait"); got.Code != 0 {
				t.Fatalf("history command: %+v", got)
			}
			screen := run("read", string(first.TerminalID))
			history := run("read", string(first.TerminalID), "--scrollback", "--lines", "100")
			if screen.Code != 0 || !strings.Contains(screen.Stdout, "history-60") || strings.Contains(screen.Stdout, "history-01") {
				t.Fatalf("screen: %+v", screen)
			}
			if history.Code != 0 || !strings.Contains(history.Stdout, "history-01") {
				t.Fatalf("scrollback: %+v", history)
			}
			limited := run("read", string(first.TerminalID), "--scrollback", "--lines", "3")
			if limited.Code != 0 || len(strings.Split(strings.TrimSpace(limited.Stdout), "\n")) > 3 {
				t.Fatalf("limited scrollback: %+v", limited)
			}

			for _, length := range []int{79, 80, 81} {
				command := fmt.Sprintf("printf '%%0%dd' 0", length)
				got := run("run", string(first.TerminalID), command, "--wait")
				if got.Code != 0 || strings.TrimSpace(got.Stdout) != strings.Repeat("0", length) {
					t.Fatalf("no-newline output length %d: %+v", length, got)
				}
				last := run("read", string(first.TerminalID), "--last")
				if last.Code != 0 || strings.TrimSpace(last.Stdout) != strings.Repeat("0", length) {
					t.Fatalf("last no-newline output length %d: %+v", length, last)
				}
			}

			sleeping := s.Launch(testworld.Invocation{Session: caller, Args: []string{"terminal", "run", string(first.TerminalID), "printf 'sleep-started\\n'; sleep 600", "--wait"}})
			app.AwaitScreen(string(first.SessionID), "sleep-started\r\n")
			if got := run("send", string(first.TerminalID), "--keys", "C-c"); got.Code != 0 {
				t.Fatalf("interrupt: %+v", got)
			}
			if got := sleeping.Wait(); got.Code != 130 {
				t.Fatalf("interrupted sleep: %+v", got)
			}

			if got := run("send", string(second.TerminalID), "--text", "exit 9\r"); got.Code != 0 {
				t.Fatalf("exit input: %+v", got)
			}
			testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == second.SessionID })
			if got := run("wait", string(second.TerminalID), "--until", "exit"); got.Code != 9 {
				t.Fatalf("wait --until exit: %+v", got)
			}

			if got := run("close", caller); got.Code != 1 || !strings.Contains(got.Stderr, "attn agent close") {
				t.Fatalf("close agent: %+v", got)
			}
			other := s.Spawn(app, fakeagent.Claude, s.Path("other"))
			s.Launched(other)
			if got := run("send", other, "--text", "unauthorized"); got.Code != 1 || !strings.Contains(got.Stderr, "delegation-chain rule") {
				t.Fatalf("outside chain: %+v", got)
			}
			if got := run("close", string(first.TerminalID)); got.Code != 0 {
				t.Fatalf("close shell: %+v", got)
			}
			t.Logf("verified terminal open/read/run/send/wait/close on %s", backend)
		})
	}
}

func TestTerminalCLIDesktopAndProfileReach(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.Start()
	app := s.App()
	caller := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(caller)
	requestID := "terminal-other-desktop"
	created := testworld.Request(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: requestID, ProfileID: app.SelectedProfile(), Name: protocol.Ptr("Ops")}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
	if !created.Success {
		t.Fatal(protocol.Deref(created.Error))
	}
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
	}
	var opened protocol.TerminalOpenResult
	got := run("open", "--desktop", "Ops", "--json", "--", "printf remote-ready")
	if got.Code != 0 {
		t.Fatal(got)
	}
	got.JSON(t, &opened)
	testworld.AwaitSession(app, string(opened.SessionID), func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	denied := run("send", string(opened.TerminalID), "--text", "x")
	if denied.Code != 1 || !strings.Contains(denied.Stderr, "--desktop "+opened.DesktopID) {
		t.Fatalf("desktop refusal: %+v", denied)
	}
	if got := run("run", string(opened.TerminalID), "printf remote-output", "--wait", "--desktop", "Ops"); got.Code != 0 || !strings.Contains(got.Stdout, "remote-output") {
		t.Fatalf("explicit desktop: %+v", got)
	}
	if got := run("read", string(opened.TerminalID)); got.Code != 0 {
		t.Fatalf("cross desktop read: %+v", got)
	}
	if got := run("close", string(opened.TerminalID), "--desktop", "Ops"); got.Code != 0 {
		t.Fatalf("cross desktop close: %+v", got)
	}

	rid := "terminal-other-profile"
	profile := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: rid, Name: "Other"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == rid })
	if !profile.Success || profile.Profile == nil {
		t.Fatalf("create profile: %+v", profile)
	}
	otherApp := s.AppOn(profile.Profile.ID)
	shell := s.Spawn(otherApp, fakeagent.Harness(protocol.SessionAgentShell), s.Path("foreign"))
	for _, ref := range []string{shell, otherApp.Terminal(shell)} {
		if got := run("read", ref); got.Code != 1 || !strings.Contains(got.Stderr, "not found") {
			t.Fatalf("foreign terminal: %+v", got)
		}
	}
	for _, args := range [][]string{{"ls", "--desktop", "Ops", "--json"}, {"ls", "--all", "--json"}} {
		if got := run(args...); got.Code != 0 || strings.Contains(got.Stdout, shell) {
			t.Fatalf("%v: %+v", args, got)
		}
	}
}

func TestTerminalCLIUsesTransitiveAuthority(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	cwd := s.Path("shop")
	root := s.Spawn(app, fakeagent.Claude, cwd)
	s.Launched(root)
	runs := map[string]*fakeagent.Run{}
	delegate := func(caller, name string) string {
		got := s.Run(testworld.Invocation{Session: caller, Args: []string{"delegate", "--brief", "Work on " + name, "--cwd", cwd, "--name", name, "--model", "default"}})
		if got.Code != 0 {
			t.Fatalf("delegate: %+v", got)
		}
		var result delegated
		got.JSON(t, &result)
		run := s.Launched(result.SessionID)
		runs[result.SessionID] = run
		run.Prompted()
		run.Reply("Ready. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, result.SessionID, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
		return result.SessionID
	}
	child := delegate(root, "builder")
	grandchild := delegate(child, "reviewer")
	typed := s.Run(testworld.Invocation{Session: root, Args: []string{"terminal", "send", grandchild, "--text", "from-grandparent\r"}})
	if typed.Code != 0 {
		t.Fatalf("transitive typing: %+v", typed)
	}
	if prompt := runs[grandchild].Prompted(); prompt != "from-grandparent" {
		t.Fatalf("typed prompt: %q", prompt)
	}
	runs[grandchild].Reply("Done. <!-- attn:state=idle -->")
	snoozed := s.Run(testworld.Invocation{Session: root, Args: []string{"session", "snooze", grandchild, "--until", "1h"}})
	if snoozed.Code != 0 {
		t.Fatalf("transitive snooze: %+v", snoozed)
	}
	moved := s.Run(testworld.Invocation{Session: root, Args: []string{"session", "move", "2", "--session", grandchild}})
	if moved.Code != 0 {
		t.Fatalf("transitive move: %+v", moved)
	}
	chiefSet, err := s.Client().CrewSet("chief", nil, protocol.Ptr("claude"), protocol.Ptr("sonnet"), nil, nil)
	if err != nil || chiefSet.WokeSessionID == nil {
		t.Fatalf("configure Chief=%+v %v", chiefSet, err)
	}
	chief := string(*chiefSet.WokeSessionID)
	s.Launched(chief)
	var rootDesktop string
	for _, desktop := range s.App().Initial.Desktops {
		for _, pane := range desktop.Panes {
			if string(pane.SessionID) == root {
				rootDesktop = desktop.ID
			}
		}
	}
	if got := s.Run(testworld.Invocation{Session: chief, Args: []string{"terminal", "send", root, "--desktop", rootDesktop, "--text", "from-chief\r"}}); got.Code != 0 {
		t.Fatalf("chief typing: %+v", got)
	}
	if got := s.Run(testworld.Invocation{Session: root, Args: []string{"agent", "close", grandchild, "-m", "Review complete"}}); got.Code != 0 {
		t.Fatalf("transitive close: %+v", got)
	}
}

func TestTerminalCLIWithoutShellIntegration(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	caller := s.Spawn(app, fakeagent.Claude, s.Path("plain-shell"))
	s.Launched(caller)
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
	}
	before := run("ls", "--json")
	refused := run("open", "--", "echo never-typed")
	if refused.Code != 1 || !strings.Contains(refused.Stderr, "No terminal was opened") {
		t.Fatalf("unsupported open: %+v", refused)
	}
	if after := run("ls", "--json"); after.Stdout != before.Stdout {
		t.Fatalf("refused open left a terminal: before %s after %s", before.Stdout, after.Stdout)
	}
	var opened protocol.TerminalOpenResult
	got := run("open", "--json")
	if got.Code != 0 {
		t.Fatal(got)
	}
	got.JSON(t, &opened)
	testworld.AwaitSession(app, string(opened.SessionID), func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	for _, args := range [][]string{{"run", string(opened.SessionID), "echo never-typed", "--wait"}, {"wait", string(opened.SessionID), "--until", "prompt"}} {
		got := run(args...)
		if got.Code != 1 || !strings.Contains(got.Stderr, "OSC 133 rule") {
			t.Fatalf("unsupported %v: %+v", args, got)
		}
	}
	if got := run("run", string(opened.TerminalID), "printf 'plain-shell-%s\\n' output"); got.Code != 0 {
		t.Fatalf("plain run: %+v", got)
	}
	app.AwaitScreen(string(opened.SessionID), "plain-shell-output")
}

func TestTerminalTextSurvivesAnUpgrade(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			s.Vars = append(s.Vars, "SHELL=/bin/bash")
			current := testworld.AttnBinaryWithSnapshotFormat(t, "terminal-text-current")
			next := testworld.AttnBinaryWithSnapshotFormat(t, "terminal-text-next")
			if backend == "shared host" {
				if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify text across shared-host handover")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating")
				s.StartBinary(current, "ATTN_PTY_HOST_BINARY="+ptyhosttest.BuildWithSnapshotFormat(t, "terminal-text-current"))
			} else {
				s.StartBinary(current)
			}
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			caller := s.Spawn(app, fakeagent.Claude, s.Path("upgrade"))
			s.Launched(caller)
			run := func(args ...string) testworld.Result {
				return s.Run(testworld.Invocation{Binary: current, Session: caller, Args: append([]string{"terminal"}, args...)})
			}
			got := run("open", "--json", "--", "printf 'upgrade-%s' ready")
			if got.Code != 0 {
				t.Fatal(got)
			}
			var opened protocol.TerminalOpenResult
			got.JSON(t, &opened)
			app.AwaitScreen(string(opened.SessionID), "upgrade-ready")
			if got := run("wait", string(opened.TerminalID), "--until", "prompt"); got.Code != 0 {
				t.Fatal(got)
			}
			output := strings.Repeat("0", 81)
			got = run("run", string(opened.TerminalID), "printf '%081d' 0; printf '\\033[?7l'", "--wait")
			if got.Code != 0 || strings.TrimSpace(got.Stdout) != output {
				t.Fatalf("before upgrade: %+v", got)
			}
			s.Stop()
			if backend == "shared host" {
				s.StartBinary(next, "ATTN_PTY_HOST_BINARY="+ptyhosttest.BuildWithSnapshotFormat(t, "terminal-text-next"))
			} else {
				s.StartBinary(next)
			}
			app = s.App()
			terminal := app.Terminal(string(opened.SessionID))
			for _, session := range app.Initial.Sessions {
				if session.ID == opened.SessionID && session.TerminalBuildStale != nil {
					testworld.AwaitSession(app, string(opened.SessionID), func(x protocol.Session) bool { return x.TerminalBuildStale == nil })
				}
			}
			attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
			if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "terminal-text-next" {
				testworld.AwaitEvent(app, "the old terminal stream to end at handover", func(e protocol.WebSocketEvent) bool {
					return e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == terminal
				})
				attached = testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
			}
			if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "terminal-text-next" {
				format := "none"
				if attached.Snapshot != nil {
					format = protocol.Deref(attached.Snapshot.Format)
				}
				t.Fatalf("worker snapshot format = %s, want terminal-text-next", format)
			}
			payload, err := base64.StdEncoding.DecodeString(attached.Snapshot.SnapshotB64)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := ghosttyvt.Restore(payload, ghosttyvt.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			restored.Write([]byte("\x1b[1;1H" + strings.Repeat("x", 81)))
			if _, row := restored.CursorPos(); row != 0 {
				t.Fatalf("the adopted terminal wraps onto row %d; wraparound was disabled before upgrade", row)
			}
			got = s.Run(testworld.Invocation{Binary: next, Session: caller, Args: []string{"terminal", "read", string(opened.SessionID), "--last"}})
			if got.Code != 0 || strings.TrimSpace(got.Stdout) != output {
				t.Fatalf("after upgrade: %+v", got)
			}
		})
	}
}
