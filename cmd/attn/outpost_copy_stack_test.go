package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type copiedSession struct {
	Label, Directory, Agent, Host string
	Priority                      bool
}

func sessionsOn(t *testing.T, s *testworld.Stack, host string) map[protocol.SessionID]copiedSession {
	t.Helper()
	app := s.App()
	sessions := app.Initial.Sessions
	app.Close()
	out := map[protocol.SessionID]copiedSession{}
	for _, session := range sessions {
		if session.Host == nil || host != "" && *session.Host != host {
			continue
		}
		out[session.ID] = copiedSession{Label: session.Label, Directory: session.Directory, Agent: session.Agent, Host: *session.Host, Priority: protocol.Deref(session.Priority)}
	}
	return out
}

func outpostLedgerRows(t *testing.T, s *testworld.Stack, all, closed bool) []string {
	t.Helper()
	app := s.App()
	defer app.Close()
	result := testworld.Request(app, protocol.SessionListMessage{Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("ledger"), All: &all, Closed: &closed}, protocol.EventSessionListResult,
		func(r protocol.SessionListResultMessage) bool { return r.RequestID == "ledger" })
	if !result.Success || result.Result == nil {
		t.Fatalf("outpost ledger: %+v", result)
	}
	rows := []string{}
	for _, row := range result.Result.Entries {
		rows = append(rows, string(row.ID))
	}
	slices.Sort(rows)
	return rows
}

func TestOutpostCopyFollowsTheHomeForItsTerminalsOnly(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	remote.Start()
	home.Start()
	pair.Connect()
	p := pair.App
	host := pair.Host

	awaitCopy := func() {
		t.Helper()
		for {
			applied.Await()
			want, got := sessionsOn(t, home, host), sessionsOn(t, remote, "")
			applied.Release()
			if reflect.DeepEqual(want, got) {
				return
			}
		}
	}
	spawnRemote := func(label, directory string) protocol.SessionID {
		t.Helper()
		return protocol.SessionID(home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), remote.Path(directory), func(msg *protocol.SpawnSessionMessage) {
			msg.Host = &host
			msg.Label = &label
		}))
	}
	remoteShell := spawnRemote("remote shell", "remote")
	home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local"))
	priority := func(on bool) {
		t.Helper()
		p.Send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: remoteShell, Priority: on})
		testworld.AwaitSession(p, string(remoteShell), func(s protocol.Session) bool { return protocol.Deref(s.Priority) == on })
	}
	awaitCopy()
	created := sessionsOn(t, remote, "")
	if _, ok := created[remoteShell]; !ok || len(created) != 1 {
		t.Fatalf("copy after create = %v, want only the shell whose host is the outpost", created)
	}

	priority(true)
	awaitCopy()
	priority(false)
	awaitCopy()
	before := sessionsOn(t, remote, "")

	remote.Stop()
	staged := remote.PauseAt(pausepoint.CopyBatchStaged)
	remote.Start()
	priority(true)
	staged.Await()
	remote.Kill()
	staged.Release()
	home.Stop()
	remote.Start()
	remote.App()
	if got := sessionsOn(t, remote, ""); !reflect.DeepEqual(got, before) {
		t.Fatalf("outpost killed mid-batch and restarted: copy %v, want the previous whole copy %v", got, before)
	}
	home.Start()
	p = home.App()
	outpostStateOnConnect(p, "outpost", protocol.OutpostStateConnected)
	staged.Await()
	staged.Release()
	awaitCopy()
	if !sessionsOn(t, remote, "")[remoteShell].Priority {
		t.Fatal("the batch the outpost lost was not sent again")
	}

	closeRemote := func(id protocol.SessionID) {
		t.Helper()
		testworld.Request(p, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: id}, protocol.EventSessionCloseResult,
			func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == id })
	}
	closeRemote(remoteShell)
	awaitCopy()
	if closed := outpostLedgerRows(t, remote, false, true); len(closed) != 0 {
		t.Fatalf("a closed shell stayed in the outpost's copy: %v", closed)
	}
	kept := spawnRemote("kept shell", "kept")
	for i := range 30 {
		closeRemote(spawnRemote(fmt.Sprintf("closed shell %d", i), "closed"))
	}
	awaitCopy()
	remote.Stop()
	remote.Start()
	staged.Await()
	staged.Release()
	awaitCopy()
	if copied := outpostLedgerRows(t, remote, true, false); !reflect.DeepEqual(copied, []string{string(kept)}) {
		t.Fatalf("after a reconnect with 31 closed shells the outpost holds %v, want only the open shell %s", copied, kept)
	}

	remote.Stop()
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	remote.Start()
	remote.App()
	if got := sessionsOn(t, remote, ""); len(got) != 0 {
		t.Fatalf("a daemon started after an offline leave kept the copy: %v", got)
	}

}

func TestReconnectRestoresTheCopyAfterAnOutpostLeaves(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	for _, s := range []*testworld.Stack{remote, home} {
		s.Start()
	}
	pair.Connect()
	p := pair.App
	host := pair.Host

	home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	awaitCopy := func() {
		t.Helper()
		for {
			applied.Await()
			equal := reflect.DeepEqual(sessionsOn(t, home, host), sessionsOn(t, remote, ""))
			applied.Release()
			if equal {
				return
			}
		}
	}
	awaitCopy()
	requireStdout(t, home.Attn("enrollment", "leave"), "not enrolled")
	if kept := sessionsOn(t, home, host); len(kept) != 1 {
		t.Fatalf("a home leaving enrollment dropped its own shell: %v", kept)
	}
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	awaitCopy()
	if copied := sessionsOn(t, remote, ""); len(copied) != 1 {
		t.Fatalf("re-pairing did not restore the home's shell: %v", copied)
	}
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
	testworld.Await(p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	if copied := sessionsOn(t, remote, ""); len(copied) != 0 {
		t.Fatalf("leave after Remove kept copied shells: %v", copied)
	}
}

func TestARemoteShellExitReachesItsHomeAfterTheOutpostStopsListening(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	prepared := home.PauseAt(pausepoint.SessionTeardownPrepared)
	reported := home.PauseEvery(pausepoint.OutpostRecoveryReported)
	applied := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	for _, s := range []*testworld.Stack{remote, home} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	pair.Connect()
	reported.Await()
	reported.Release()
	applied.Await()
	applied.Release()
	app := pair.App
	host := pair.Host

	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	release, exiting := makeFifo(t, remote.Path("release.fifo")), makeFifo(t, remote.Path("exiting.fifo"))
	app.TypeLine(id, "echo armed; read line < "+release+"; echo exiting > "+exiting+"; exit 0")
	app.AwaitScreen(id, "armed")

	mark := len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"))
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, mark)
	remote.Stop()
	handled := remote.PauseAt(pausepoint.PtyExitHandled)
	remote.Start()
	writeFifo(t, release, "go\n")
	readFifo(t, exiting)
	handled.Await()
	handled.Release()

	mark = len(app.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"))
	prepared.Await()
	reported.Await()
	reported.Release()
	prepared.Release()
	applied.Await()
	applied.Release()
	testworld.AwaitSince(app, mark, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == id && e.ExitCode == 0 })
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return s.TerminalExit != nil && s.TerminalExit.Code == 0 })
	testworld.AwaitSince(app, mark, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == id })
	requireStdout(t, home.Attn("session", "show", id), "state      closed", " by "+id, "because    Agent exited normally")
	if rows := outpostLedgerRows(t, remote, false, true); slices.Contains(rows, id) {
		t.Fatalf("the outpost closed its copy of %s itself: %v", id, rows)
	}
}

func TestARemoteShellThatOutlivesItsCloseEndsOnlyOnItsOutpost(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	handled := remote.PauseAt(pausepoint.PtyExitHandled)
	for _, s := range []*testworld.Stack{remote, home} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	pair.Connect()
	app := pair.App
	host := pair.Host
	outpost := remote.App()

	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	app.TypeLine(id, "trap '' TERM; echo armed")
	app.AwaitScreen(id, "armed")

	testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)}, protocol.EventSessionCloseResult,
		func(r protocol.SessionCloseResultMessage) bool { return string(r.SessionID) == id })
	for {
		applied.Await()
		live := slices.Contains(outpostLedgerRows(t, remote, false, false), id)
		applied.Release()
		if !live {
			break
		}
	}
	if copied := outpostLedgerRows(t, remote, true, false); slices.Contains(copied, id) {
		t.Fatalf("the outpost kept its copy of %s after the home closed it: %v", id, copied)
	}
	handled.Await()
	handled.Release()
	gone := testworld.Request(outpost, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if gone.Success && protocol.Deref(gone.Running) {
		t.Fatalf("the shell's PTY still runs on the outpost after its exit: %+v", gone)
	}
	if exits := exitsOf(t, outpost, id); exits != 0 {
		t.Fatalf("the outpost ran the home part of %s's exit %d times, want none", id, exits)
	}
	requireStdout(t, home.Attn("session", "show", id), "state      closed", " by user")
}

func exitsOf(t *testing.T, p *testworld.Peer, id string) int {
	t.Helper()
	exits := 0
	for _, raw := range p.Log() {
		var event struct {
			Event     string `json:"event"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == protocol.EventSessionExited && event.SessionID == id {
			exits++
		}
	}
	return exits
}

func makeFifo(t *testing.T, path string) string {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFifo(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFifo(t *testing.T, path string) {
	t.Helper()
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestARemoteShellExitingAsItsHomeClosesItEndsOnlyOnItsOutpost(t *testing.T) {
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, remote)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	resolved := remote.PauseAt(pausepoint.PtyExitOwnerResolved)
	handled := remote.PauseAt(pausepoint.PtyExitHandled)
	for _, s := range []*testworld.Stack{remote, home} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	pair.Connect()
	app := pair.App
	host := pair.Host
	outpost := remote.App()

	id := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	terminal := protocol.TerminalID(app.Terminal(id))
	release := makeFifo(t, remote.Path("release.fifo"))
	app.TypeLine(id, "echo armed; read line < "+release+"; exit 0")
	app.AwaitScreen(id, "armed")

	writeFifo(t, release, "go\n")
	resolved.Await()
	testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(id)}, protocol.EventSessionCloseResult,
		func(r protocol.SessionCloseResultMessage) bool { return string(r.SessionID) == id })
	for {
		applied.Await()
		copied := slices.Contains(outpostLedgerRows(t, remote, true, false), id)
		applied.Release()
		if !copied {
			break
		}
	}
	resolved.Release()
	handled.Await()
	handled.Release()
	testworld.Request(outpost, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if exits := exitsOf(t, outpost, id); exits != 0 {
		t.Fatalf("the outpost ran the home part of %s's exit %d times, want none", id, exits)
	}
}
