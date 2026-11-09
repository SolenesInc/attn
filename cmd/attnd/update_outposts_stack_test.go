package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/testworld"
)

type outpostedInstall struct {
	*sourceInstall
	ssh    *testworld.SSH
	remote *testworld.Stack
	host   string
}

const outpostedDestination = "update-outpost"

func newOutpostedInstall(t *testing.T, beforeUpdate func(*outpostedInstall, *testworld.Peer), vars ...string) *outpostedInstall {
	t.Helper()
	return addOutpostToInstall(t, &outpostedInstall{sourceInstall: newSourceInstall(t, vars...), ssh: testworld.NewSSH(t), remote: testworld.NewStack(t)}, beforeUpdate)
}

func addOutpostToInstall(t *testing.T, install *outpostedInstall, beforeUpdate func(*outpostedInstall, *testworld.Peer)) *outpostedInstall {
	t.Helper()
	old := filepath.Join(install.tree, "bin", "attn")
	install.remote.RunFrom(old)
	install.ssh.Use(install.stack.World)
	install.ssh.Route(outpostedDestination, install.remote)
	install.stack.InstallsOnOutposts(old, "")
	install.stack.Start()
	for _, key := range []string{setting.SelfUpdateEnabled.Key, "outposts_enabled"} {
		if result := install.stack.Attn("settings", "set", key, "true"); result.Code != 0 {
			t.Fatalf("turn on %s: %+v", key, result)
		}
	}
	app := install.stack.App()
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: outpostedDestination})
	row := awaitOutpost(app, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
	install.host = row.ID
	if beforeUpdate != nil {
		beforeUpdate(install, app)
	}
	install.stack.Stop()
	install.stack.InstallsOnOutposts(install.target, "")
	install.expectRestart()
	return install
}

func (i *outpostedInstall) startHome() *testworld.Peer {
	i.stack.Start()
	app := i.stack.App()
	i.stack.FreeWSPort()
	awaitOutpost(app, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
	return app
}

func awaitOutpost(app *testworld.Peer, done func(protocol.OutpostInfo) bool) protocol.OutpostInfo {
	var found protocol.OutpostInfo
	match := func(rows []protocol.OutpostInfo) bool {
		for _, row := range rows {
			if done(row) {
				found = row
				return true
			}
		}
		return false
	}
	if !match(app.Initial.Outposts) {
		testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return match(m.Outposts) })
	}
	return found
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func onlyOutpost(t *testing.T, run protocol.UpdateRun) protocol.UpdateOutpostProgress {
	t.Helper()
	if len(run.Outposts) != 1 {
		t.Fatalf("run outposts: %+v", run.Outposts)
	}
	return run.Outposts[0]
}

func TestAnOutpostSwitchesOnlyAfterTheHomeRestartedAndKeepsItsShell(t *testing.T) {
	var shell string
	install := newOutpostedInstall(t, func(i *outpostedInstall, app *testworld.Peer) {
		host := i.host
		shell = i.stack.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), i.remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	})
	staged := install.stack.PauseAt(pausepoint.UpdateOutpostsStaged)
	app := install.startHome()
	pidPath := filepath.Join(install.remote.Dir, "shell.pid")
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo before-$((1+1))")
	app.AwaitScreen(shell, "before-2")
	before := fileBytes(t, pidPath)
	installPath := install.ssh.InstallPath(install.remote)
	oldOutpost := fileBytes(t, installPath)

	startUpdate(t, app)
	release := staged.Take()
	if !bytes.Equal(fileBytes(t, installPath), oldOutpost) {
		t.Fatal("the outpost changed before the home restarted")
	}
	stagedAttnd, err := filepath.Glob(filepath.Join(filepath.Dir(installPath), ".attnd-install-*"))
	if err != nil || len(stagedAttnd) != 1 {
		t.Fatalf("staged attnd: %v %v", stagedAttnd, err)
	}
	release()

	app = install.restarted()
	run := finalRun(app)
	if run.Phase != protocol.UpdatePhaseDone || onlyOutpost(t, run).State != protocol.UpdateOutpostStateDone {
		t.Fatalf("run: %+v", run)
	}
	if !bytes.Equal(fileBytes(t, installPath), fileBytes(t, install.target)) {
		t.Fatal("the outpost is not on the home's build after the run")
	}
	row := awaitOutpost(app, func(row protocol.OutpostInfo) bool {
		return row.State == protocol.OutpostStateConnected && strings.Contains(protocol.Deref(row.Build), "2.0.0")
	})
	app.TypeLine(shell, "echo $$ > "+pidPath+"; echo after-$((2+2))")
	app.AwaitScreen(shell, "after-4")
	if after := fileBytes(t, pidPath); !bytes.Equal(before, after) {
		t.Fatalf("shell PID changed %s -> %s", before, after)
	}
	t.Logf("outpost on %s, shell PID %s", protocol.Deref(row.Build), strings.TrimSpace(string(before)))
}

func TestAnUnreachableOutpostIsSkippedAndTheRunFinishes(t *testing.T) {
	install := newOutpostedInstall(t, nil)
	app := install.startHome()
	installPath := install.ssh.InstallPath(install.remote)
	oldOutpost := fileBytes(t, installPath)
	install.ssh.Fail(outpostedDestination, "no route to host")
	startUpdate(t, app)
	app = install.restarted()
	run := finalRun(app)
	if run.Phase != protocol.UpdatePhaseDone || onlyOutpost(t, run).State != protocol.UpdateOutpostStateSkipped || !strings.Contains(protocol.Deref(run.Outcome), "Skipped outpost") {
		t.Fatalf("run: %+v", run)
	}
	if !bytes.Equal(fileBytes(t, installPath), oldOutpost) {
		t.Fatal("the skipped outpost changed")
	}
}

func TestAStagingFailureStopsBeforeTheHomeRestartsAndUpdateWithoutItCompletes(t *testing.T) {
	install := newOutpostedInstall(t, nil)
	install.ssh.MachineCommand(install.remote, "chmod", "echo staging-denied >&2; exit 1")
	app := install.startHome()
	installPath := install.ssh.InstallPath(install.remote)
	oldOutpost := fileBytes(t, installPath)
	oldHome := fileBytes(t, filepath.Join(install.tree, "bin", "attnd"))
	startUpdate(t, app)
	failed := finalRun(app)
	outpost := onlyOutpost(t, failed)
	if failed.Phase != protocol.UpdatePhaseFailed || outpost.State != protocol.UpdateOutpostStateFailed || !strings.Contains(protocol.Deref(outpost.Detail), "staging-denied") || !strings.Contains(protocol.Deref(failed.Outcome), "did not restart") {
		t.Fatalf("run: %+v", failed)
	}
	if !bytes.Equal(fileBytes(t, filepath.Join(install.tree, "bin", "attnd")), oldHome) {
		t.Fatal("the home was replaced after a staging failure")
	}

	mark := len(app.Log())
	app.Send(protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate, RequestID: protocol.Ptr("without"), WithoutOutposts: []string{outpost.ID}})
	result := testworld.AwaitSince(app, mark, protocol.EventStartUpdateResult, func(protocol.StartUpdateResultMessage) bool { return true })
	if !result.Success {
		t.Fatalf("Update without it: %+v", result)
	}
	app = install.restarted()
	run := finalRun(app)
	if run.Phase != protocol.UpdatePhaseDone || onlyOutpost(t, run).State != protocol.UpdateOutpostStateSkipped {
		t.Fatalf("run: %+v", run)
	}
	if !bytes.Equal(fileBytes(t, installPath), oldOutpost) {
		t.Fatal("the left-out outpost changed")
	}
}

func TestDuringTheHomeRestartOutpostCallsNameTheUpdateAndAnswerStatusLocally(t *testing.T) {
	dir := t.TempDir()
	reached, releaseRestart := filepath.Join(dir, "reached"), filepath.Join(dir, "release")
	for _, fifo := range []string{reached, releaseRestart} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared := &outpostedInstall{sourceInstall: newSourceInstall(t, "RESTART_REACHED="+reached, "RESTART_RELEASE="+releaseRestart), ssh: testworld.NewSSH(t), remote: testworld.NewStack(t)}
	install := addOutpostToInstall(t, prepared, func(i *outpostedInstall, app *testworld.Peer) {
		requested := i.remote.Attn("update", "request", "--reason", "from the outpost")
		if requested.Code != 0 {
			t.Fatalf("outpost update request: %+v", requested)
		}
		offered := updateRequestNotifications(app)
		if len(offered) != 1 {
			t.Fatalf("home notification: %+v", offered)
		}
	})
	app := install.startHome()
	startUpdate(t, app)
	if _, err := os.ReadFile(reached); err != nil {
		t.Fatal(err)
	}

	local := install.stack.Attn("seed", "ls")
	if local.Code == 0 || !strings.Contains(local.Stderr, "attn is restarting to apply an update") {
		t.Fatalf("local CLI during its daemon's restart: %+v", local)
	}
	forwarded := install.remote.Attn("seed", "ls")
	if forwarded.Code == 0 || !strings.Contains(forwarded.Stderr, "restarting to update attn to") || !(strings.Contains(forwarded.Stderr, "did not run") || strings.Contains(forwarded.Stderr, "may have run")) {
		t.Fatalf("forwarded call during the restart: %+v", forwarded)
	}
	status := install.remote.Attn("update", "status", "--json")
	if status.Code != 0 {
		t.Fatalf("local status: %+v", status)
	}
	var answered protocol.UpdateStatus
	if err := json.Unmarshal([]byte(status.Stdout), &answered); err != nil {
		t.Fatalf("status %q: %v", status.Stdout, err)
	}
	if answered.Run == nil || !strings.Contains(answered.Run.TargetBuild, "2.0.0") {
		t.Fatalf("status names no update: %+v", answered)
	}

	if err := os.WriteFile(releaseRestart, nil, 0); err != nil {
		t.Fatal(err)
	}
	app = install.restarted()
	if run := finalRun(app); run.Phase != protocol.UpdatePhaseDone {
		t.Fatalf("run: %+v", run)
	}
	after := install.remote.Run(testworld.Invocation{Binary: install.ssh.InstallPath(install.remote), Args: []string{"seed", "ls"}})
	if after.Code != 0 || strings.Contains(after.Stdout+after.Stderr, "update") {
		t.Fatalf("call after reconnect: %+v", after)
	}
}

func TestARunThatFailsWithTheHomeStillUpClearsTheNoticeAndTheMarker(t *testing.T) {
	install := newOutpostedInstall(t, nil, "FAIL_INSTALL=1")
	app := install.startHome()
	started := startUpdate(t, app)
	install.finished.Take()()
	if run := finalRun(app); run.ID != started.ID || run.Phase != protocol.UpdatePhaseFailed {
		t.Fatalf("run: %+v", run)
	}
	if _, err := os.Stat(filepath.Join(install.stack.Dir, "update-restarting")); !os.IsNotExist(err) {
		t.Fatalf("restart marker left behind: %v", err)
	}
	status := install.remote.Attn("update", "status", "--json")
	var answered protocol.UpdateStatus
	if err := json.Unmarshal([]byte(status.Stdout), &answered); err != nil {
		t.Fatalf("status %+v: %v", status, err)
	}
	if answered.Run == nil || answered.Run.Phase != protocol.UpdatePhaseFailed || answered.HomeKind == nil {
		t.Fatalf("outpost status is not the home's failed run: %+v", answered)
	}
}
