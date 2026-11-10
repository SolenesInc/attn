package main_test

import (
	"os"
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
