package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAWorkerUpgradedInPlaceKeepsItsProgramBlocksAndTerminalSettings(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	next := testworld.AttnBinaryWithSnapshotFormat(t, "next-format")
	s.StartWith("ATTN_KITTY_STORAGE_LIMIT=0")
	app := s.App()
	app.Send(protocol.SetTerminalThemeMessage{Cmd: protocol.CmdSetTerminalTheme, Foreground: "#405060", Background: "#102030", Cursor: "#708090"})
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: protocol.TerminalID(app.Terminal(shell)), Cols: 91, Rows: 23})
	pidFile := filepath.Join(s.Dir, "shell.pid")
	app.TypeLine(shell, `printf '\033]133;A\007$ \033]133;C;cmdline=before-upgrade\007\033]133;D;0\007'; echo $$ > `+pidFile+`; echo recorded-$((1+1))`)
	app.AwaitScreen(shell, "recorded-2")
	before, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()

	s.StartBinary(next)
	app = s.App()
	terminal := app.Terminal(shell)
	attach := func() protocol.AttachResultMessage {
		return testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	}
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == shell && x.TerminalBuildStale != nil {
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.TerminalBuildStale == nil })
		}
	}
	attached := attach()
	if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "next-format" {
		t.Fatalf("after the restart the session attached with snapshot %+v, want the worker swapped to next-format", attached.Snapshot)
	}
	var carried bool
	for _, block := range attached.Snapshot.Blocks {
		carried = carried || (protocol.Deref(block.Command) == "before-upgrade" && protocol.Deref(block.ExitCode) == 0)
	}
	if !carried {
		t.Errorf("the upgraded worker's blocks are %+v, want the block recorded before the upgrade", attached.Snapshot.Blocks)
	}

	probe := filepath.Join(s.Dir, "probe.sh")
	if err := os.WriteFile(probe, []byte(`stty size
printf '\033]11;?\007\033_Ga=T,f=24,s=2,v=2,i=77;AQIDBAUGBwgJCgsM\033\\'
IFS= read -rs -d '\' bg
printf 'bg=%s=\n' "${bg#??}"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(shell, `echo $$ > `+pidFile+`; bash `+probe)
	app.AwaitScreen(shell, "bg=11;rgb:1010/2020/3030")
	after, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(before)) {
		t.Errorf("the shell was pid %s before the upgrade and %s after, want the same process", before, after)
	}
	app.AwaitScreen(shell, "23 91")
	if image := testworld.Request(app, protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: protocol.TerminalID(terminal), ImageID: 77},
		protocol.EventKittyImageResult, func(r protocol.KittyImageResultMessage) bool { return r.ImageID == 77 }); image.Success {
		t.Errorf("the upgraded worker stored image 77, want images still off as the session was launched")
	}
	app.TypeLine(shell, "exit 7")
	exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == shell })
	if exited.ExitCode != 7 {
		t.Errorf("the shell exited %d under the upgraded worker, want 7", exited.ExitCode)
	}
}

func TestAWorkerRefusingAnUpgradeKeepsRunningItsProgramAndStaysStale(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	next := testworld.AttnBinaryWithSnapshotFormat(t, "next-format")
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	pidFile := filepath.Join(s.Dir, "shell.pid")
	app.TypeLine(shell, `echo $$ > `+pidFile+`; echo started-$((1+1))`)
	app.AwaitScreen(shell, "started-2")
	before, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()

	notAttn := filepath.Join(s.Dir, "not-attn")
	if err := os.WriteFile(notAttn, []byte("not an executable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.StartBinary(next, "ATTN_PTY_WORKER_BINARY="+notAttn)
	app = s.App()
	if !protocol.Deref(initialSession(t, app, shell).TerminalBuildStale) {
		testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.TerminalBuildStale) })
	}
	app.TypeLine(shell, `echo $$ > `+pidFile+`; echo still-$((2+2))`)
	app.AwaitScreen(shell, "still-4")
	after, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(before)) {
		t.Errorf("the shell was pid %s before the refused upgrade and %s after, want the same process", before, after)
	}
}

func TestWithInPlaceUpgradesOffAnUpdatedTerminalKeepsItsOldWorkerAndShowsTheReloadNotice(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	next := testworld.AttnBinaryWithSnapshotFormat(t, "next-format")
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	app.TypeLine(shell, "echo started-$((1+1))")
	app.AwaitScreen(shell, "started-2")
	s.Stop()

	s.StartBinary(next, "ATTN_WORKER_INPLACE_UPGRADE=0")
	app = s.App()
	if came := initialSession(t, app, shell); !protocol.Deref(came.TerminalBuildStale) {
		t.Errorf("the terminal came back without the reload notice, want it offered while in-place upgrades are off")
	}
	terminal := app.Terminal(shell)
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) == "next-format" {
		t.Fatalf("the terminal attached with snapshot %+v, want its worker still on the old build", attached.Snapshot)
	}
	app.TypeLine(shell, "echo still-$((2+2))")
	app.AwaitScreen(shell, "still-4")
}

func TestAnAgentWhoseTerminalEndedWhileTheDaemonWasDownShowsNoReloadNotice(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	codex.Reply("Done. <!-- attn:state=idle -->")
	s.Stop()
	codex.Exit(143)

	s.Start()
	if came := initialSession(t, s.App(), session); came.TerminalBuildStale != nil {
		t.Errorf("the agent without a terminal came back with terminal_build_stale=%t, want no reload notice", *came.TerminalBuildStale)
	}
}

func TestASharedHostTerminalShowsNoReloadNoticeAfterAnUpdate(t *testing.T) {
	t.Parallel()
	host := os.Getenv("ATTN_TEST_PTY_HOST")
	if host == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	s := testworld.NewStack(t)
	next := testworld.AttnBinaryWithSnapshotFormat(t, "next-format")
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=shared", "ATTN_PTY_HOST_BINARY="+host)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	app.TypeLine(shell, "echo started-$((1+1))")
	app.AwaitScreen(shell, "started-2")
	s.Stop()

	s.StartBinary(next)
	app = s.App()
	if came := initialSession(t, app, shell); came.TerminalBuildStale != nil {
		t.Errorf("the shared-host terminal came back with terminal_build_stale=%t, want it replayed without a reload notice", *came.TerminalBuildStale)
	}
	terminal := app.Terminal(shell)
	testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	app.TypeLine(shell, "echo still-$((2+2))")
	app.AwaitScreen(shell, "still-4")
}

func initialSession(t *testing.T, app *testworld.Peer, id string) protocol.Session {
	t.Helper()
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == id {
			return x
		}
	}
	t.Fatalf("session %s is missing from the initial state", id)
	return protocol.Session{}
}
