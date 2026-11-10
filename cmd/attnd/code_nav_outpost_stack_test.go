package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/procwait"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var runningServer = regexp.MustCompile(`go in (\S+): ready, pid (\d+)`)

func goServerPID(t *testing.T, stack *testworld.Stack) int {
	t.Helper()
	status := stack.Attn("nav", "status")
	found := runningServer.FindStringSubmatch(status.Stdout)
	if found == nil {
		t.Fatalf("nav status shows no ready go server:\n%s%s", status.Stdout, status.Stderr)
	}
	pid, _ := strconv.Atoi(found[2])
	return pid
}

// The fake language server keeps running when its stdin closes, as a server
// stuck in its own work does, so only the next daemon's reap ends it.
func TestAnOutpostsReviewNavigatesOnTheOutpostAndItsNextStartReapsAServerAKilledDaemonLeft(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	repo := filepath.Join(outpost.Path("work"), "repo")
	reviewedRepository(t, repo, "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n", "package calc\n\nfunc Add(a, b int) int {\n\treturn b + a\n}\n\nfunc Twice(a int) int {\n\treturn Add(a, a)\n}\n")
	document := filepath.Join(outpost.Path("work"), "review.md")
	review := "---\nattn: review\nrepository: " + repo + "\nbase: main\nhead: main\n---\n# Calc\n\n### calc.go:3-5\n"
	if err := os.WriteFile(document, []byte(review), 0o644); err != nil {
		t.Fatal(err)
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	copied := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "code_review_enabled", "true"), "true")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "code_review_enabled"
	})
	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), outpost.Path("installed"))
	requireStdout(t, outpost.Attn("nav", "link", fake), "linked")

	id := uuid.NewString()
	answer := testworld.Request(pair.App, protocol.CodeNavMessage{Cmd: protocol.CmdCodeNav, Host: protocol.Ptr(pair.Host), Document: document, Path: "calc.go", Line: 8, Character: 8, Kind: "definition", RequestID: protocol.Ptr(id)},
		protocol.EventCodeNavResult, func(r protocol.CodeNavResultMessage) bool { return protocol.Deref(r.RequestID) == id })
	if !answer.Success || len(answer.Locations) != 1 || answer.Locations[0].Path != "calc.go" || answer.Locations[0].Line != 3 {
		t.Fatalf("code_nav on the outpost answered %+v (error %s), want calc.go:3", answer.Locations, protocol.Deref(answer.Error))
	}
	pid := goServerPID(t, outpost)
	requireStdout(t, home.Attn("nav", "status"), "no language server is running on this machine")
	requireStdout(t, outpost.Run(testworld.Invocation{Args: []string{"nav", "check", "calc.go:8"}, Dir: repo}), "gopls: Add -> calc.go:3")

	outpost.Kill()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the language server %d ended with its daemon (%v); the test needs one that outlives it", pid, err)
	}
	outpost.Start()
	if exited, err := procwait.Exited(pid, fakeagent.HangGuard); err != nil || !exited {
		t.Fatalf("the language server %d a killed daemon left still runs %s after the next start: %v", pid, fakeagent.HangGuard, err)
	}
	if status := outpost.Attn("nav", "status"); !strings.Contains(status.Stdout, "no language server is running") {
		t.Fatalf("after the restart nav status shows\n%s\nwant no server", status.Stdout)
	}
}

func TestAnOutpostStopsTheLanguageServerOfAReviewTheHomeShowedWhenTheHomeIsGoneAndWatchesItAgainOnReconnect(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	repo := filepath.Join(outpost.Path("work"), "repo")
	reviewedRepository(t, repo, "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n", "package calc\n\nfunc Add(a, b int) int {\n\treturn b + a\n}\n\nfunc Twice(a int) int {\n\treturn Add(a, a)\n}\n")
	document := filepath.Join(outpost.Path("work"), "review.md")
	review := "---\nattn: review\nrepository: " + repo + "\nbase: main\nhead: main\n---\n# Calc\n\n### calc.go:3-5\n"
	if err := os.WriteFile(document, []byte(review), 0o644); err != nil {
		t.Fatal(err)
	}
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	copied := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "code_review_enabled", "true"), "true")
	requireStdout(t, home.Attn("settings", "set", "code_navigation_idle_stop_seconds", "0"), "0")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "code_navigation_idle_stop_seconds"
	})
	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), outpost.Path("installed"))
	requireStdout(t, outpost.Attn("nav", "link", fake), "linked")

	app := pair.App
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	app.TypeLine(shell, "attn open "+document+" && printf '__opened_%s\\n' yes")
	app.AwaitScreen(shell, "__opened_yes")
	awaitOutpostTileContent(app, pair.Host, review)
	ask := func() {
		t.Helper()
		id := uuid.NewString()
		answer := testworld.Request(app, protocol.CodeNavMessage{Cmd: protocol.CmdCodeNav, Host: protocol.Ptr(pair.Host), Document: document, Path: "calc.go", Line: 8, Character: 8, Kind: "definition", RequestID: protocol.Ptr(id)},
			protocol.EventCodeNavResult, func(r protocol.CodeNavResultMessage) bool { return protocol.Deref(r.RequestID) == id })
		if !answer.Success || len(answer.Locations) != 1 || answer.Locations[0].Line != 3 {
			t.Fatalf("code_nav on the outpost answered %+v (error %s), want calc.go:3", answer.Locations, protocol.Deref(answer.Error))
		}
	}
	ask()
	pid := goServerPID(t, outpost)

	pair.Disconnect()
	outpost.FirePending(bridge.LostTimer)
	if exited, err := procwait.Exited(pid, fakeagent.HangGuard); err != nil || !exited {
		t.Fatalf("the language server %d of a review the home showed still runs %s after the home's bridge dropped: %v", pid, fakeagent.HangGuard, err)
	}

	pair.Reconnect()
	edited := review + "\nStill adds.\n"
	if err := os.WriteFile(document, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	awaitOutpostTileContent(app, pair.Host, edited)
	ask()
}

func reviewNavigation(review *protocol.ReviewBundle, language string) protocol.ReviewNavigation {
	if review != nil {
		for _, entry := range review.Navigation {
			if entry.Language == language {
				return entry
			}
		}
	}
	return protocol.ReviewNavigation{}
}

func awaitOutpostNavigation(app *testworld.Peer, host, document, language string, match func(protocol.ReviewNavigation) bool) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return protocol.Deref(m.Host) == host && m.Path == document && match(reviewNavigation(m.Review, language))
	})
}

func inState(state string) func(protocol.ReviewNavigation) bool {
	return func(entry protocol.ReviewNavigation) bool { return entry.State == state }
}

func showOnOutpost(home *testworld.Stack, pair *testworld.OutpostPair, dir, document string) {
	home.T.Helper()
	shell := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), dir, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	pair.App.TypeLine(shell, "attn open "+document+" && printf '__opened_%s\\n' yes")
	pair.App.AwaitScreen(shell, "__opened_yes")
}

func enableCodeReview(t *testing.T, home *testworld.Stack, remote *testworld.Peer, copied int) {
	t.Helper()
	requireStdout(t, home.Attn("settings", "set", "code_review_enabled", "true"), "true")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "code_review_enabled"
	})
}

func TestSetupFromAnOutpostsReviewRunsItsAgentOnTheOutpostAndAPassingNavCheckThereMakesTheReviewReady(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	repo := filepath.Join(outpost.Path("work"), "repo")
	reviewedRepository(t, repo, "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n", "package calc\n\nfunc Add(a, b int) int {\n\treturn b + a\n}\n\nfunc Twice(a int) int {\n\treturn Add(a, a)\n}\n")
	document := filepath.Join(outpost.Path("work"), "review.md")
	if err := os.WriteFile(document, []byte("---\nattn: review\nrepository: "+repo+"\nbase: main\nhead: main\n---\n# Calc\n\n### calc.go:3-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	enableCodeReview(t, home, remote, remote.EventCount())
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)

	showOnOutpost(home, pair, outpost.Path("shell"), document)
	shown := awaitOutpostNavigation(pair.App, pair.Host, document, "go", inState("missing"))
	missing := reviewNavigation(shown.Review, "go")
	id := uuid.NewString()
	setup := testworld.Request(pair.App, protocol.CodeNavSetupMessage{Cmd: protocol.CmdCodeNavSetup, RequestID: protocol.Ptr(id), DesktopID: shown.DesktopID, Host: protocol.Ptr(pair.Host), Document: document, Repository: repo, Language: "go", File: missing.File, State: missing.State, Detail: missing.Detail},
		protocol.EventCodeNavSetupResult, func(r protocol.CodeNavSetupResultMessage) bool { return protocol.Deref(r.RequestID) == id })
	if !setup.Success {
		t.Fatalf("setup of an outpost's review answered %+v (error %s), want the session it started", setup, protocol.Deref(setup.Error))
	}
	session := string(protocol.Deref(setup.SessionID))
	if agent := testworld.AwaitSession(pair.App, session, func(protocol.Session) bool { return true }); protocol.Deref(agent.Host) != pair.Host {
		t.Errorf("the setup agent runs on %q, want the review's machine %s", protocol.Deref(agent.Host), pair.Host)
	}
	awaitOutpostNavigation(pair.App, pair.Host, document, "go", func(entry protocol.ReviewNavigation) bool {
		return string(protocol.Deref(entry.SetupSessionID)) == session
	})

	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), outpost.Path("installed"))
	requireStdout(t, outpost.Attn("nav", "link", fake), "linked")
	requireStdout(t, outpost.Run(testworld.Invocation{Args: []string{"nav", "check", "calc.go:8"}, Dir: repo}), "gopls: Add -> calc.go:3")
	awaitOutpostNavigation(pair.App, pair.Host, document, "go", inState("ready"))
}

func TestAnOutpostsReviewOfALanguageWithNoDefinitionTurnsReadyOnceTheHomeSetsOne(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	repo := filepath.Join(outpost.Path("work"), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Calc.java"), []byte("class Calc {\n  int twice(int a) {\n    return a + a;\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://example.test/calc.git"}, {"add", "Calc.java"}, {"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-qm", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	document := filepath.Join(outpost.Path("work"), "review.md")
	if err := os.WriteFile(document, []byte("---\nattn: review\nrepository: "+repo+"\nbase: main\nhead: main\n---\n# Twice\n\n### Calc.java:2-4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	enableCodeReview(t, home, remote, remote.EventCount())

	showOnOutpost(home, pair, outpost.Path("shell"), document)
	awaitOutpostNavigation(pair.App, pair.Host, document, "java", inState("missing"))
	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), outpost.Path("installed"))
	requireStdout(t, home.Run(testworld.Invocation{Args: []string{"nav", "set", "java", `{"command": ["` + fake + `"], "extensions": [".java"]}`}, Dir: repo}), "java")
	awaitOutpostNavigation(pair.App, pair.Host, document, "java", inState("ready"))
}
