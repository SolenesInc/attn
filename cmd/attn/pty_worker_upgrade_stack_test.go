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
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: shell, Cols: 91, Rows: 23})
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
	attach := func() protocol.AttachResultMessage {
		return testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shell},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == shell })
	}
	for _, x := range app.Initial.Sessions {
		if x.ID == shell && x.TerminalBuildStale != nil {
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
	if image := testworld.Request(app, protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: shell, ImageID: 77},
		protocol.EventKittyImageResult, func(r protocol.KittyImageResultMessage) bool { return r.ImageID == 77 }); image.Success {
		t.Errorf("the upgraded worker stored image 77, want images still off as the session was launched")
	}
	app.TypeLine(shell, "exit 7")
	exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == shell })
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
	testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return protocol.Deref(x.TerminalBuildStale) })
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
