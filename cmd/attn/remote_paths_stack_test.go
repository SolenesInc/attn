package main_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// Both daemons share one disk here, so every directory a remote shell uses
// also exists on the home: the case where reading the home's disk would
// find a folder with the same name and act on it.
func TestARemoteShellsDirectoryIsNeverReadOnTheHomesDisk(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(app, "outpost", protocol.OutpostStateConnected).ID
	onOutpost := func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(host) }
	shell := fakeagent.Harness(protocol.SessionAgentShell)

	repo := home.Path("shop")
	gitRepo(t, repo)
	worktree := home.Path("shop--feat")
	created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat", Path: protocol.Ptr(worktree)},
		protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.Path) == worktree })
	if !created.Success {
		t.Fatalf("create worktree: %s", protocol.Deref(created.Error))
	}
	elsewhere, local := home.Path("remote-only"), home.Path("local")
	remoteInWorktree := home.Spawn(app, shell, worktree, onOutpost)
	localInWorktree := home.Spawn(app, shell, worktree)
	remoteElsewhere := home.Spawn(app, shell, elsewhere, onOutpost)
	localElsewhere := home.Spawn(app, shell, local)
	for _, id := range []string{remoteInWorktree, localInWorktree, remoteElsewhere, localElsewhere} {
		app.TypeLine(id, "echo up-$((1+1))")
		app.AwaitScreen(id, "up-2")
	}

	recent := testworld.Request(app, protocol.GetRecentLocationsMessage{Cmd: protocol.CmdGetRecentLocations, RequestID: protocol.Ptr("recent")},
		protocol.EventRecentLocationsResult, func(r protocol.RecentLocationsResultMessage) bool { return protocol.Deref(r.RequestID) == "recent" })
	recorded := map[string]bool{}
	for _, location := range recent.RecentLocations {
		recorded[filepath.Base(location.Path)] = true
	}
	if recorded["remote-only"] || !recorded["local"] {
		t.Errorf("recent locations = %+v; want the local shell's folder and never the remote shell's", recent.RecentLocations)
	}

	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: worktree},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == worktree })
	if !deleted.Success {
		t.Fatalf("delete worktree: %s", protocol.Deref(deleted.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == localInWorktree
	})
	sessions, err := home.Client().Query("")
	if err != nil {
		t.Fatal(err)
	}
	if !querySessionsHas(sessions, remoteInWorktree) || querySessionsHas(sessions, localInWorktree) {
		t.Errorf("after deleting the home's worktree, sessions = %+v; want the remote shell kept and the local one closed", sessions)
	} else {
		app.TypeLine(remoteInWorktree, "echo still-$((2+2))")
		app.AwaitScreen(remoteInWorktree, "still-4")
	}

	t.Logf("recent=%v remote kept after worktree delete", recorded)
}

func querySessionsHas(sessions []protocol.Session, id string) bool {
	for _, s := range sessions {
		if string(s.ID) == id {
			return true
		}
	}
	return false
}
