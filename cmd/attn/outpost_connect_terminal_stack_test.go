package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSSHConnectInATerminalSurvivesHomeRestartAndConnectsAfterAnAnswer(t *testing.T) {
	home, remote := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	t.Cleanup(func() { remote.Attn("daemon", "stop") })
	pair.SSH.Route(pair.Destination, remote)
	pair.SSH.RequireConfirmation(pair.Destination)
	home.Start()
	logSSHFailure(t, home, remote, pair.SSH)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: pair.Destination})
	row := outpostState(app, "outpost", protocol.OutpostStateRefused)
	if !protocol.Deref(row.Connectable) || protocol.Deref(row.LastError) != "Host key verification failed.\n" {
		t.Fatalf("refusal: %+v", row)
	}
	app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "connect-outpost"})
	opened := testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.SessionRegisteredMessage) bool { return m.Session.Label == "Connect to outpost" }).Session
	if opened.Agent != protocol.AgentShellValue || opened.Host != nil || !protocol.Deref(opened.CommandTerminal) {
		t.Fatalf("connection terminal must be a home shell: %+v", opened)
	}
	app.AwaitScreen(string(opened.ID), "Are you sure you want to continue connecting (yes/no)?")
	result := testworld.Await(app, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "connect-outpost" })
	if !result.Success || protocol.Deref(result.PaneID) == "" || len(result.Desktops) != 1 {
		t.Fatalf("connection tile result: %+v", result)
	}
	app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "reuse-outpost"})
	reused := testworld.Await(app, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "reuse-outpost" })
	if !reused.Success || protocol.Deref(reused.PaneID) != protocol.Deref(result.PaneID) || len(reused.Desktops) != 1 || reused.Desktops[0].ID != result.Desktops[0].ID {
		t.Fatalf("reuse returned another tile: %+v -> %+v", result, reused)
	}
	assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
	before := pair.SSH.MasterAttempts()
	home.Stop()
	home.Start()
	app = home.App()
	if got := pair.SSH.MasterAttempts(); got != before {
		t.Fatalf("background master created during prompt across restart: %d -> %d", before, got)
	}
	app.AwaitScreen(string(opened.ID), "Are you sure you want to continue connecting (yes/no)?")
	assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
	app.TypeLine(string(opened.ID), "yes")
	testworld.Await(app, protocol.EventSessionExited, func(m protocol.SessionExitedMessage) bool {
		return m.SessionID == opened.ID && m.ExitCode == 0
	})
	outpostState(app, "outpost", protocol.OutpostStateConnected)
	fresh := home.App()
	found := false
	for _, session := range fresh.Initial.Sessions {
		if session.ID == opened.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("successful connection terminal disappeared before the user closed it")
	}
	assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
	resultList := testworld.Request(app, protocol.SessionListMessage{Cmd: protocol.CmdSessionList, All: protocol.Ptr(true), RequestID: protocol.Ptr("all-shells")}, protocol.EventSessionListResult, func(m protocol.SessionListResultMessage) bool { return m.RequestID == "all-shells" })
	if !resultList.Success {
		t.Fatalf("ledger: %+v", resultList)
	}
	for _, desktop := range fresh.Initial.Desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID != opened.ID {
				continue
			}
			if desktop.ActivePaneID != pane.PaneID {
				t.Fatal("connection terminal was not focused")
			}
			close := testworld.Request(app, protocol.DesktopCloseTileMessage{Cmd: protocol.CmdDesktopCloseTile, DesktopID: desktop.ID, TileID: pane.PaneID, RequestID: "close-connection"}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "close-connection" })
			if !close.Success {
				t.Fatalf("close connection tile: %+v", close)
			}
		}
	}
	assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
	t.Log("ssh stderr and action -> focused home shell; same prompt after home restart; yes -> exit 0 -> connected without another action; tile retained until close; no live or closed ledger row")
}

func assertConnectionTerminalAbsentFromLedger(t *testing.T, app *testworld.Peer, id protocol.SessionID) {
	t.Helper()
	result := testworld.Request(app, protocol.SessionListMessage{Cmd: protocol.CmdSessionList, All: protocol.Ptr(true), RequestID: protocol.Ptr("connection-ledger")}, protocol.EventSessionListResult, func(m protocol.SessionListResultMessage) bool { return m.RequestID == "connection-ledger" })
	if !result.Success {
		t.Fatalf("ledger: %+v", result)
	}
	for _, entry := range result.Result.Entries {
		if entry.ID == id {
			t.Fatalf("connection terminal appeared in ledger: %+v", entry)
		}
	}
}

func TestSSHConnectionTerminalFailureCanBeRetried(t *testing.T) {
	home, remote := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	pair.SSH.Route(pair.Destination, remote)
	pair.SSH.RequireConfirmation(pair.Destination)
	t.Cleanup(func() { remote.Attn("daemon", "stop") })
	home.Start()
	logSSHFailure(t, home, remote, pair.SSH)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: pair.Destination})
	row := outpostState(app, "outpost", protocol.OutpostStateRefused)
	open := func() protocol.Session {
		mark := app.EventCount()
		app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "connect-outpost"})
		opened := testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(m protocol.SessionRegisteredMessage) bool { return m.Session.Label == "Connect to outpost" }).Session
		app.AwaitScreen(string(opened.ID), "Are you sure you want to continue connecting (yes/no)?")
		return opened
	}
	first := open()
	app.TypeLine(string(first.ID), "no")
	testworld.Await(app, protocol.EventSessionExited, func(m protocol.SessionExitedMessage) bool { return m.SessionID == first.ID && m.ExitCode == 255 })
	failed := home.App().Initial.Outposts[0]
	if failed.State != protocol.OutpostStateRefused || !protocol.Deref(failed.Connectable) {
		t.Fatalf("failed attempt lost its action: %+v", failed)
	}
	next := open()
	if next.ID == first.ID {
		t.Fatal("retry reused exited terminal")
	}
	resume := pair.SSH.Pause(pair.Destination)
	defer resume()
	mark := app.EventCount()
	app.TypeLine(string(next.ID), "yes")
	connecting := outpostStateSince(app, "outpost", protocol.OutpostStateConnecting, mark)
	if protocol.Deref(connecting.LastError) != protocol.Deref(row.LastError) || protocol.Deref(connecting.LastErrorAt) != protocol.Deref(row.LastErrorAt) {
		t.Fatalf("connection cleared the last error before success: before=%+v after=%+v", row, connecting)
	}
	resume()
	outpostStateSince(app, "outpost", protocol.OutpostStateConnected, mark)
	assertConnectionTerminalAbsentFromLedger(t, app, first.ID)
	assertConnectionTerminalAbsentFromLedger(t, app, next.ID)
	t.Log("no -> exit 255 keeps refusal and tile; another attempt accepts yes and connects")
}

func TestSSHConnectionTerminalIsCancelledByOffAndRemove(t *testing.T) {
	for _, action := range []string{"off", "remove"} {
		t.Run(action, func(t *testing.T) {
			home, remote := testworld.NewStack(t), testworld.NewStack(t)
			pair := testworld.PrepareOutpostPair(t, home, remote)
			pair.SSH.Route(pair.Destination, remote)
			pair.SSH.RequireConfirmation(pair.Destination)
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: pair.Destination})
			row := outpostState(app, "outpost", protocol.OutpostStateRefused)
			app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "connect-outpost"})
			opened := testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.SessionRegisteredMessage) bool { return m.Session.Label == "Connect to outpost" }).Session
			app.AwaitScreen(string(opened.ID), "Are you sure you want to continue connecting (yes/no)?")
			if action == "off" {
				if result := home.Attn("settings", "set", "outposts_enabled", "false"); result.Code != 0 {
					t.Fatalf("off: %+v", result)
				}
			} else {
				app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
				testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
				warning := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool { return protocol.Deref(m.Cmd) == protocol.CmdRemoveOutpost })
				if !strings.Contains(warning.Error, "may still be running") {
					t.Fatalf("missing remote-stop warning while authentication is unanswered: %+v", warning)
				}
				t.Log(warning.Error)
			}
			testworld.Await(app, protocol.EventSessionUnregistered, func(m protocol.SessionUnregisteredMessage) bool { return m.Session.ID == opened.ID })
			for _, session := range home.App().Initial.Sessions {
				if session.ID == opened.ID {
					t.Fatal("connection process remains after cancellation")
				}
			}
			assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
			t.Log(action + " cancels the pending ssh prompt and removes its tile")
		})
	}
}

func TestSSHConnectionTerminalMayExitBeforeItsTileIsOpened(t *testing.T) {
	for _, success := range []bool{false, true} {
		name := "refused"
		if success {
			name = "connected"
		}
		t.Run(name, func(t *testing.T) {
			home, remote := testworld.NewStack(t), testworld.NewStack(t)
			pair := testworld.PrepareOutpostPair(t, home, remote)
			pair.SSH.Route(pair.Destination, remote)
			pair.SSH.Fail(pair.Destination, "Host key verification failed.")
			t.Cleanup(func() { remote.Attn("daemon", "stop") })
			home.Start()
			app := enableOutposts(t, home)
			app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: pair.Destination})
			row := outpostState(app, "outpost", protocol.OutpostStateRefused)
			if success {
				pair.SSH.Restore(pair.Destination)
			}
			app.Send(protocol.ConnectOutpostInTerminalMessage{Cmd: protocol.CmdConnectOutpostInTerminal, ID: row.ID, RequestID: "connect-outpost"})
			opened := testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.SessionRegisteredMessage) bool { return m.Session.Label == "Connect to outpost" }).Session
			exited := testworld.Await(app, protocol.EventSessionExited, func(m protocol.SessionExitedMessage) bool { return m.SessionID == opened.ID })
			want := 255
			if success {
				want = 0
				outpostState(app, "outpost", protocol.OutpostStateConnected)
			}
			if exited.ExitCode != want {
				t.Fatalf("exit code %d, want %d", exited.ExitCode, want)
			}
			fresh := home.App()
			retained := false
			for _, session := range fresh.Initial.Sessions {
				if session.ID == opened.ID {
					retained = true
				}
			}
			if !retained {
				t.Fatal("immediate exit removed connection tile")
			}
			assertConnectionTerminalAbsentFromLedger(t, app, opened.ID)
		})
	}
}

func logSSHFailure(t *testing.T, home, remote *testworld.Stack, ssh *testworld.SSH) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, name := range []string{"calls.jsonl", "routes.json"} {
			data, err := os.ReadFile(filepath.Join(ssh.Dir, name))
			t.Logf("ssh %s err=%v:\n%s", name, err, data)
		}
		t.Logf("current outpost snapshot: %+v", home.App().Initial.Outposts)
		remote.LogDaemonTail()
	})
}
