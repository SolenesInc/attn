package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/testworld"
)

// appMachineWorld is a Linux home and an app machine that joined it by dialing it, both on one build at first.
type appMachineWorld struct {
	t            *testing.T
	ssh          *testworld.SSH
	home, laptop *testworld.Stack
	homeApp      *testworld.Peer
	row          protocol.OutpostInfo
	name         string
}

// newAppMachineWorld starts both machines on build; arm runs before they start, to arm their pause points.
func newAppMachineWorld(t *testing.T, build string, arm ...func(*appMachineWorld)) *appMachineWorld {
	t.Helper()
	w := &appMachineWorld{t: t, ssh: testworld.NewSSH(t), home: testworld.NewStack(t), laptop: testworld.NewStack(t)}
	for _, s := range []*testworld.Stack{w.home, w.laptop} {
		s.RunFrom(build)
		w.ssh.Use(s.World)
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
	}
	for _, armed := range arm {
		armed(w)
	}
	for _, s := range []*testworld.Stack{w.home, w.laptop} {
		s.Start()
	}
	w.ssh.Route("home", w.home)
	w.homeApp = enableOutposts(t, w.home)
	for _, s := range []*testworld.Stack{w.home, w.laptop} {
		for _, key := range []string{"remote_home_enabled", "self_update_enabled"} {
			requireStdout(t, s.Attn("settings", "set", key, "true"), "true")
		}
	}
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	w.name = name
	mark := w.homeApp.EventCount()
	requireStdout(t, w.laptop.Attn("enrollment", "join", "home"), "dial:       home (bridged)")
	if machine := w.laptop.MachineApp(); machine.Initial.Settings["self_update_enabled"] != "true" {
		testworld.Await(machine, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["self_update_enabled"] == "true" })
	}
	w.row = outpostStateSince(w.homeApp, w.name, protocol.OutpostStateConnected, mark)
	t.Cleanup(func() {
		if t.Failed() {
			for _, s := range []*testworld.Stack{w.home, w.laptop} {
				s.LogDaemonTail()
			}
		}
	})
	return w
}

// installOnLaptop restarts the app machine on build, which it also pushes as its own Linux build. The channel tells
// each run the app machine finishes.
func (w *appMachineWorld) installOnLaptop(build string) <-chan struct{} {
	w.t.Helper()
	w.laptop.Stop()
	w.laptop.RunFrom(build)
	w.laptop.InstallsOnOutposts(build, "")
	finished := w.laptop.PassesOf(pausepoint.UpdateRunFinished)
	w.laptop.Start()
	return finished
}

// finishedRunOf waits until the app machine s finishes a run, and reads it the way attn update status does on s
// while its home refuses it.
func finishedRunOf(t *testing.T, s *testworld.Stack, finished <-chan struct{}) protocol.UpdateRun {
	t.Helper()
	<-finished
	status := updateStatusOf(t, s)
	if status.Run == nil {
		t.Fatal("attn update status on the app machine shows no run after one finished")
	}
	return *status.Run
}

// addBox starts an ssh outpost of the home on build and returns it with its row.
func (w *appMachineWorld) addBox(build string) (*testworld.Stack, protocol.OutpostInfo) {
	w.t.Helper()
	box := testworld.NewStack(w.t)
	box.RunFrom(build)
	box.Vars = append(box.Vars, "SHELL=/bin/bash")
	box.Start()
	w.ssh.Route("box", box)
	w.homeApp.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "box", Destination: "box"})
	return box, outpostState(w.homeApp, "box", protocol.OutpostStateConnected)
}

type pidShell struct {
	id, pidPath, before string
}

func (w *appMachineWorld) spawnShell(on *testworld.Stack, host *string, name string) pidShell {
	w.t.Helper()
	id := w.home.Spawn(w.homeApp, fakeagent.Harness(protocol.SessionAgentShell), on.Path(name), func(m *protocol.SpawnSessionMessage) { m.Host = host })
	shell := pidShell{id: id, pidPath: on.Path(name + ".pid")}
	w.homeApp.TypeLine(id, "echo $$ > "+shell.pidPath+"; echo "+name+"-$((1+1))")
	w.homeApp.AwaitScreen(id, name+"-2")
	shell.before = shellPid(w.t, shell.pidPath)
	return shell
}

func (w *appMachineWorld) requireSameShell(app *testworld.Peer, shell pidShell, word string) {
	w.t.Helper()
	app.TypeLine(shell.id, "echo $$ > "+shell.pidPath+"; echo "+word+"-$((2+2))")
	app.AwaitScreen(shell.id, word+"-4")
	if got := shellPid(w.t, shell.pidPath); got != shell.before {
		w.t.Fatalf("shell %s changed pid across the update: %s -> %s", shell.id, shell.before, got)
	}
}

// relayedRun connects an app to the app machine s, as the attn app does, until s relays it to its home, and returns
// the home's run that seen accepts. While s cannot reach its home it answers home_unreachable and closes the
// connection at the next change of its dial, so the app connects again.
func relayedRun(t *testing.T, s *testworld.Stack, seen func(protocol.UpdateRun) bool) protocol.UpdateRun {
	t.Helper()
	for {
		p := s.ConnectApp()
		first := testworld.AwaitEvent(p, "initial_state or home_unreachable", func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventInitialState || e.Event == protocol.EventHomeUnreachable
		})
		if first.Event == protocol.EventHomeUnreachable {
			p.Closed()
			p.Close()
			continue
		}
		var initial protocol.InitialStateMessage
		for _, raw := range p.Log() {
			if json.Unmarshal(raw, &initial) == nil && initial.Event == protocol.EventInitialState {
				break
			}
		}
		if run := initial.UpdateRun; run != nil && seen(*run) {
			return *run
		}
		return testworld.Await(p, protocol.EventUpdateChanged, func(m protocol.UpdateChangedMessage) bool { return seen(m.Run) }).Run
	}
}

func finishedRun(kind string) func(protocol.UpdateRun) bool {
	return func(run protocol.UpdateRun) bool {
		return run.InstallKind == kind && (run.Phase == protocol.UpdatePhaseDone || run.Phase == protocol.UpdatePhaseFailed)
	}
}

func homeRunOf(t *testing.T, app *testworld.Peer, kind string) protocol.UpdateRun {
	t.Helper()
	finished := func(run protocol.UpdateRun) bool {
		return run.InstallKind == kind && (run.Phase == protocol.UpdatePhaseDone || run.Phase == protocol.UpdatePhaseFailed)
	}
	if run := app.Initial.UpdateRun; run != nil && finished(*run) {
		return *run
	}
	return testworld.Await(app, protocol.EventUpdateChanged, func(m protocol.UpdateChangedMessage) bool { return finished(m.Run) }).Run
}

func machineRowOf(t *testing.T, run protocol.UpdateRun, role protocol.UpdateMachineRole) protocol.UpdateOutpostProgress {
	t.Helper()
	for _, row := range run.Outposts {
		if protocol.Deref(row.Role) == role {
			return row
		}
	}
	t.Fatalf("run %s has no %s row: %+v", run.ID, role, run.Outposts)
	return protocol.UpdateOutpostProgress{}
}

func requireSameFile(t *testing.T, what, got, want string) {
	t.Helper()
	if !bytes.Equal(homeFileBytes(t, got), homeFileBytes(t, want)) {
		t.Fatalf("%s at %s is not %s", what, got, want)
	}
}

func homeFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnAppMachineOnANewBuildUpdatesItsHomeByItselfOnce(t *testing.T) {
	w := newAppMachineWorld(t, testworld.AttnBinary(t))
	homeShell := w.spawnShell(w.home, nil, "home-shell")

	next := testworld.AttnUpdateBinary(t, "2.0.0-app-machine", "source", "")
	w.installOnLaptop(next)
	w.home.AwaitDaemonReplaced()
	w.homeApp = w.home.App()
	fleet := homeRunOf(t, w.homeApp, "fleet")
	home := machineRowOf(t, fleet, protocol.UpdateMachineRoleHome)
	if fleet.Phase != protocol.UpdatePhaseDone || home.State != protocol.UpdateOutpostStateDone || !strings.Contains(protocol.Deref(home.Build), "2.0.0-app-machine") || protocol.Deref(machineRowOf(t, fleet, protocol.UpdateMachineRoleAppMachine).Build) != protocol.Deref(home.Build) {
		t.Fatalf("the home's run after the app machine updated it: %+v %+v", fleet, fleet.Outposts)
	}
	requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), next)
	requireSameFile(t, "the home's attnd", filepath.Join(filepath.Dir(w.ssh.InstallPath(w.home)), "attnd"), filepath.Join(filepath.Dir(next), "attnd"))
	if seen := relayedRun(t, w.laptop, func(run protocol.UpdateRun) bool { return run.ID == fleet.ID }); seen.Phase != protocol.UpdatePhaseDone {
		t.Fatalf("the app on the app machine shows %+v", seen)
	}
	t.Logf("home run: %s", protocol.Deref(fleet.Outcome))
	w.requireSameShell(w.homeApp, homeShell, "home-after")
	requireStdout(t, w.laptop.Attn("update", "status"), "home        ", "app machine ")
}

func updateStatusOf(t *testing.T, s *testworld.Stack) protocol.UpdateStatus {
	t.Helper()
	result := s.Attn("update", "status", "--json")
	if result.Code != 0 {
		t.Fatalf("attn update status on the app machine exited %d: %s", result.Code, result.Stderr)
	}
	var status protocol.UpdateStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		t.Fatalf("attn update status --json printed %q: %v", result.Stdout, err)
	}
	return status
}

// redial lets the app machine's dialer run count more times, and returns once it waits again.
func (w *appMachineWorld) redial(count int) {
	w.t.Helper()
	for range count {
		w.laptop.AwaitTimer(bridge.RetryTimer)
		w.laptop.FirePending(bridge.RetryTimer)
	}
	w.laptop.AwaitTimer(bridge.RetryTimer)
}

func TestAFailedSwitchOfTheHomeStartsNoSecondUpdateAcrossReconnects(t *testing.T) {
	w := newAppMachineWorld(t, testworld.AttnBinary(t))
	installed := w.ssh.InstallPath(w.home)
	kept := filepath.Join(filepath.Dir(installed), ptyhost.BinaryName+".previous", "kept")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	before := homeFileBytes(t, installed)

	next := testworld.AttnUpdateBinary(t, "2.0.0-switch-fails", "source", "")
	run := finishedRunOf(t, w.laptop, w.installOnLaptop(next))
	home := machineRowOf(t, run, protocol.UpdateMachineRoleHome)
	if run.Phase != protocol.UpdatePhaseFailed || home.State != protocol.UpdateOutpostStateFailed || protocol.Deref(home.Reason) != protocol.UpdateFailureReasonRefused || protocol.Deref(home.Action) != "Update" || !strings.Contains(protocol.Deref(home.Detail), ptyhost.BinaryName+".previous") {
		t.Fatalf("a home whose switch fails: %+v %+v", run, home)
	}
	t.Logf("home row: %s (%s): %s; %s", home.State, protocol.Deref(home.Reason), protocol.Deref(home.Detail), protocol.Deref(home.Action))

	w.redial(3)
	status := updateStatusOf(t, w.laptop)
	if status.Run == nil || status.Run.ID != run.ID || status.Run.Phase != protocol.UpdatePhaseFailed {
		t.Fatalf("after three refused redials the latest run is %+v, not the failed run %s", status.Run, run.ID)
	}
	if !bytes.Equal(homeFileBytes(t, installed), before) {
		t.Fatal("a redial after the failed switch installed on the home")
	}
	requireStdout(t, w.laptop.Attn("update", "status"), "home        ", "(refused)", "; Update")

	app := w.laptop.MachineApp()
	if shown := app.Initial.UpdateRun; shown == nil || shown.ID != run.ID || protocol.Deref(machineRowOf(t, *shown, protocol.UpdateMachineRoleHome).Action) != "Update" {
		t.Fatalf("the app reads %+v from the app machine, not the failed run %s with Update", shown, run.ID)
	}
	if err := os.RemoveAll(filepath.Dir(kept)); err != nil {
		t.Fatal(err)
	}
	app.Send(protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate, RequestID: protocol.Ptr("update")})
	if started := testworld.Await(app, protocol.EventStartUpdateResult, func(protocol.StartUpdateResultMessage) bool { return true }); !started.Success {
		t.Fatalf("Update from the app machine's own daemon: %+v", started)
	}
	w.home.AwaitDaemonReplaced()
	if fleet := homeRunOf(t, w.home.App(), "fleet"); fleet.Phase != protocol.UpdatePhaseDone {
		t.Fatalf("the home after Update: %+v", fleet)
	}
	requireSameFile(t, "the home's attn", installed, next)
}

func TestAnAppMachineWhoseBuildDidNotChangeStartsNoUpdateOfAHomeAhead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		base   func(t *testing.T) string
		next   func(t *testing.T) string
		action func(next string) string
		detail string
	}{
		{
			name:   "release",
			base:   func(t *testing.T) string { return testworld.AttnBinary(t) },
			next:   func(t *testing.T) string { return testworld.AttnUpdateBinary(t, "2.0.0-ahead-release", "source", "") },
			action: func(string) string { return "/releases/tag/v2.0.0-ahead-release" },
		},
		{
			name:   "commit",
			base:   func(t *testing.T) string { return testworld.AttnUpdateBinary(t, "1.0.0-behind-commit", "source", "") },
			next:   func(t *testing.T) string { return testworld.AttnUpdateBinary(t, "2.0.0-ahead-commit", "source", "") },
			action: func(string) string { return "git checkout 2.0.0-ahead-commit && make install" },
		},
		{
			name:   "changed-worktree",
			base:   func(t *testing.T) string { return testworld.AttnUpdateBinary(t, "1.0.0-behind-tree", "source", "") },
			next:   func(t *testing.T) string { return testworld.AttnWorktreeBinary(t, "2.0.0-ahead-tree") },
			detail: "build it again from the changed worktree that built it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base(t)
			w := newAppMachineWorld(t, base)
			second := testworld.NewStack(t)
			second.RunFrom(base)
			w.ssh.Use(second.World)
			secondFinished := second.PassesOf(pausepoint.UpdateRunFinished)
			second.Start()
			for _, key := range []string{"remote_home_enabled", "self_update_enabled"} {
				requireStdout(t, second.Attn("settings", "set", key, "true"), "true")
			}
			requireStdout(t, second.Attn("enrollment", "join", "home"), "dial:       home (bridged)")

			next := tc.next(t)
			w.installOnLaptop(next)
			w.home.AwaitDaemonReplaced()
			if fleet := homeRunOf(t, w.home.App(), "fleet"); fleet.Phase != protocol.UpdatePhaseDone {
				t.Fatalf("the first app machine's update: %+v", fleet)
			}
			stop := second.FireTimers(bridge.RetryTimer)
			run := finishedRunOf(t, second, secondFinished)
			stop()
			app := machineRowOf(t, run, protocol.UpdateMachineRoleAppMachine)
			action := protocol.Deref(app.Action)
			if protocol.Deref(app.Reason) != protocol.UpdateFailureReasonBehind {
				t.Fatalf("the second app machine, behind its home: %+v", app)
			}
			if tc.action == nil && action != "" || tc.action != nil && !strings.HasSuffix(action, tc.action(next)) {
				t.Fatalf("the second app machine's way to catch up: %q", action)
			}
			if !strings.Contains(protocol.Deref(app.Detail), tc.detail) {
				t.Fatalf("the second app machine's row says %q, not %q", protocol.Deref(app.Detail), tc.detail)
			}
			requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), next)
			requireStdout(t, second.Attn("update", "status"), "(behind)", action)
		})
	}
}

func TestAnAppMachineInstallingABuildWhileItsHomeRunsAnUnsharedBuildStartsNoUpdate(t *testing.T) {
	w := newAppMachineWorld(t, testworld.AttnBinary(t))
	other := testworld.AttnUpdateBinary(t, "2.0.0-home-elsewhere", "source", "")
	w.home.Stop()
	w.home.RunFrom(other)
	w.ssh.InstallBinary(w.home, other)
	w.home.Start()

	finished := w.installOnLaptop(testworld.AttnUpdateBinary(t, "2.0.0-laptop-elsewhere", "source", ""))
	stop := w.laptop.FireTimers(bridge.RetryTimer)
	run := finishedRunOf(t, w.laptop, finished)
	stop()
	if run.Phase != protocol.UpdatePhaseFailed || !strings.Contains(run.TargetBuild, "2.0.0-laptop-elsewhere") {
		t.Fatalf("the app machine's run: %+v", run)
	}
	app := machineRowOf(t, run, protocol.UpdateMachineRoleAppMachine)
	home := machineRowOf(t, run, protocol.UpdateMachineRoleHome)
	if app.State != protocol.UpdateOutpostStateSkipped || protocol.Deref(app.Action) != "Update" || !strings.Contains(protocol.Deref(home.Build), "2.0.0-home-elsewhere") {
		t.Fatalf("an app machine whose home runs a build they never shared: %+v %+v", app, home)
	}
	requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), other)
	t.Log(protocol.Deref(run.Outcome))
}

func TestAFleetRunCutByTheNextSwitchOfTheHomeDoesNotStandInForTheNextOne(t *testing.T) {
	base := testworld.AttnBinary(t)
	var fleetStarted *testworld.Pause
	w := newAppMachineWorld(t, base, func(w *appMachineWorld) { fleetStarted = w.home.PauseAt(pausepoint.FleetUpdateStarted) })
	box, _ := w.addBox(base)

	w.installOnLaptop(testworld.AttnUpdateBinary(t, "2.0.0-first", "source", ""))
	fleetStarted.Take()
	w.home.AwaitDaemonReplaced()
	recovery := w.home.PauseAt(pausepoint.DaemonStartupRecovery)
	second := testworld.AttnUpdateBinary(t, "2.0.0-second", "source", "")
	<-w.installOnLaptop(second)
	recovery.Take()()
	fleetStarted.Take()()
	fleet := homeRunOf(t, w.home.App(), "fleet")
	boxRow := machineRowOf(t, fleet, protocol.UpdateMachineRoleOutpost)
	if fleet.Phase != protocol.UpdatePhaseDone || !strings.Contains(fleet.TargetBuild, "2.0.0-second") || boxRow.State != protocol.UpdateOutpostStateDone {
		t.Fatalf("the home's run for its outposts after the second switch: %s %s %q, box %+v", fleet.Phase, fleet.TargetBuild, protocol.Deref(fleet.Outcome), boxRow)
	}
	requireSameFile(t, "the outpost's attn", w.ssh.InstallPath(box), second)
	box.AwaitDaemonReplaced()
}

func TestAFleetUpdateWhoseSourceMachineLeftNamesItOnTheOutpostsRow(t *testing.T) {
	base := testworld.AttnBinary(t)
	var fleetStarted *testworld.Pause
	w := newAppMachineWorld(t, base, func(w *appMachineWorld) { fleetStarted = w.home.PauseAt(pausepoint.FleetUpdateStarted) })
	box, _ := w.addBox(base)
	before := homeFileBytes(t, w.ssh.InstallPath(box))
	boxBuild := updateStatusOf(t, box)

	w.installOnLaptop(testworld.AttnUpdateBinary(t, "2.0.0-source-left", "source", ""))
	resume := fleetStarted.Take()
	w.home.AwaitDaemonReplaced()
	w.laptop.Stop()
	w.ssh.MachineCommand(box, "uname", "echo Linux aarch64")
	resume()
	fleet := homeRunOf(t, w.home.App(), "fleet")
	row := machineRowOf(t, fleet, protocol.UpdateMachineRoleOutpost)
	detail := protocol.Deref(row.Detail)
	if row.State != protocol.UpdateOutpostStateFailed || protocol.Deref(row.Reason) != protocol.UpdateFailureReasonNoBuild || !strings.Contains(detail, w.name) || !strings.Contains(detail, "linux/arm64") || protocol.Deref(row.Action) != "Update outpost" || !strings.Contains(protocol.Deref(row.Build), boxBuild.Version) {
		t.Fatalf("the outpost whose platform only the away source machine builds: %+v (%s)", row, detail)
	}
	status := w.home.Attn("update", "status")
	requireStdout(t, status, "(no build)", w.name, "; Update outpost")
	t.Log(strings.TrimSpace(status.Stdout))
	if !bytes.Equal(homeFileBytes(t, w.ssh.InstallPath(box)), before) {
		t.Fatal("the outpost got an install")
	}
}

// releaseOf serves binary as the Linux assets of version, with a host that cannot run.
func releaseOf(t *testing.T, version string, binary *string) string {
	t.Helper()
	assets := http.NewServeMux()
	assets.HandleFunc("/v"+version+"/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		switch {
		case strings.HasPrefix(name, "attnd-linux-"):
			http.ServeFile(w, r, filepath.Join(filepath.Dir(*binary), "attnd"))
		case strings.HasPrefix(name, "attn-linux-"):
			http.ServeFile(w, r, *binary)
		case strings.HasPrefix(name, ptyhost.BinaryName+"-linux-"):
			w.Write([]byte("#!/bin/sh\nexit 1\n"))
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(assets)
	t.Cleanup(server.Close)
	return server.URL
}

func TestOneUpdateBringsTheHomeAndThenItsOutpostToTheAppMachinesBuild(t *testing.T) {
	for _, strategy := range []string{"committed", "worktree", "release"} {
		t.Run(strategy, func(t *testing.T) {
			var next string
			switch strategy {
			case "committed":
				next = testworld.AttnUpdateBinary(t, "2.0.0-committed", "source", "")
			case "worktree":
				next = testworld.AttnWorktreeBinary(t, "2.0.0-worktree")
			case "release":
				next = testworld.AttnUpdateBinary(t, "2.0.0", "release", releaseOf(t, "2.0.0", &next))
			}
			base := testworld.AttnBinary(t)
			w := newAppMachineWorld(t, base)
			box, boxRow := w.addBox(base)
			homeShell := w.spawnShell(w.home, nil, "home-shell")
			boxShell := w.spawnShell(box, &boxRow.ID, "box-shell")

			w.laptop.Stop()
			w.laptop.RunFrom(next)
			if strategy != "release" {
				w.laptop.InstallsOnOutposts(next, "")
			}
			w.laptop.Start()
			w.home.AwaitDaemonReplaced()
			seen := relayedRun(t, w.laptop, finishedRun("fleet"))
			if carried := machineRowOf(t, seen, protocol.UpdateMachineRoleOutpost); seen.Phase != protocol.UpdatePhaseDone || carried.Name != "box" || carried.State != protocol.UpdateOutpostStateDone {
				t.Fatalf("the app on the app machine shows the home's run as %s with its outpost %+v", seen.Phase, carried)
			}
			w.homeApp = w.home.App()
			fleet := homeRunOf(t, w.homeApp, "fleet")
			boxProgress := machineRowOf(t, fleet, protocol.UpdateMachineRoleOutpost)
			if fleet.Phase != protocol.UpdatePhaseDone || boxProgress.State != protocol.UpdateOutpostStateDone {
				for _, row := range fleet.Outposts {
					t.Logf("row %s %s %s: %s (%s)", protocol.Deref(row.Role), row.Name, row.State, protocol.Deref(row.Detail), protocol.Deref(row.Reason))
				}
				t.Fatalf("the home's run for its outpost: %s %s", fleet.Phase, protocol.Deref(fleet.Outcome))
			}
			requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), next)
			requireSameFile(t, "the outpost's attn", w.ssh.InstallPath(box), next)
			box.AwaitDaemonReplaced()
			if _, err := os.Stat(filepath.Join(w.home.Dir, "source")); !os.IsNotExist(err) {
				t.Fatalf("the home has a checkout: %v", err)
			}
			w.requireSameShell(w.homeApp, homeShell, "home-after")
			w.requireSameShell(w.homeApp, boxShell, "box-after")
			t.Logf("fleet run: %s", protocol.Deref(fleet.Outcome))

			requireStdout(t, w.home.Attn("daemon", "stop"))
			w.home.RunFrom(w.ssh.InstallPath(w.home))
			requireStdout(t, w.home.Attn("daemon", "ensure"))
			status := w.home.Attn("update", "status", "--json")
			if status.Code != 0 || !strings.Contains(status.Stdout, "2.0.0") {
				t.Fatalf("the home started again from its install path is not on the new build: %+v", status)
			}

			if strategy == "release" {
				return
			}
			w.laptop.Stop()
			arm := testworld.NewStack(t)
			arm.RunFrom(base)
			w.ssh.Route("arm-box", arm)
			w.ssh.MachineCommand(arm, "uname", "echo Linux aarch64")
			if err := os.Remove(w.ssh.InstallPath(arm)); err != nil {
				t.Fatal(err)
			}
			w.homeApp = w.home.App()
			w.homeApp.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "arm-box", Destination: "arm-box"})
			added := awaitOutpostInstallNamed(w.homeApp, "arm-box")
			if added.Update.Phase != protocol.OutpostUpdatePhaseFailed || !strings.Contains(protocol.Deref(added.Update.Outcome), "holds the source of this build") || !strings.Contains(protocol.Deref(added.Update.Outcome), "linux/arm64") {
				t.Fatalf("an arm64 outpost with the source machine away: %+v", added.Update)
			}
			if _, err := os.Stat(w.ssh.InstallPath(arm)); !os.IsNotExist(err) {
				t.Fatalf("the arm64 outpost got an install: %v", err)
			}
			t.Log(protocol.Deref(added.Update.Outcome))
		})
	}
}

func awaitOutpostInstallNamed(app *testworld.Peer, name string) protocol.OutpostInfo {
	var found protocol.OutpostInfo
	match := func(rows []protocol.OutpostInfo) bool {
		for _, row := range rows {
			if row.Name == name && row.Update != nil && (row.Update.Phase == protocol.OutpostUpdatePhaseDone || row.Update.Phase == protocol.OutpostUpdatePhaseFailed) {
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

type homeUpdateFailure struct {
	name   string
	role   protocol.UpdateMachineRole
	reason protocol.UpdateFailureReason
	cause  string
	// arrange runs with the app machine stopped, before it starts on next; it returns the build the app machine sends.
	arrange func(t *testing.T, w *appMachineWorld, next string) (sends string)
	// fail runs while the update waits to stage, for faults the dial itself would hit.
	fail func(t *testing.T, w *appMachineWorld)
	// staged runs once the home holds the staged build, before it switches.
	staged func(t *testing.T, w *appMachineWorld)
}

func TestEveryFailedHomeUpdateNamesItsReasonCauseAndActionOnTheRightRow(t *testing.T) {
	for _, tc := range []homeUpdateFailure{
		{name: "compile-failure", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonNoBuild, cause: "build outpost linux/amd64"},
		{name: "release-download-failure", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonNoBuild, cause: "404"},
		{name: "staged-build-differs", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonRefused, cause: "staged build",
			arrange: func(t *testing.T, w *appMachineWorld, next string) string {
				return testworld.AttnUpdateBinary(t, "2.0.0-another", "source", "")
			}},
		{name: "wrong-platform", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonRefused, cause: "Linux amd64 or arm64",
			arrange: func(t *testing.T, w *appMachineWorld, next string) string {
				w.ssh.MachineCommand(w.home, "uname", "echo Darwin arm64")
				return next
			}},
		{name: "newer-database", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonRefused, cause: "contains migration",
			arrange: func(t *testing.T, w *appMachineWorld, next string) string {
				recordMigrationFromANewerBuild(t, filepath.Join(w.home.Dir, "attn.db"))
				return next
			}},
		{name: "home-unreachable", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonUnreachable, cause: "Connection refused",
			fail: func(t *testing.T, w *appMachineWorld) {
				w.ssh.Fail("home", "ssh: connect to host home port 22: Connection refused")
			}},
		{name: "switched-not-back", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonNotBack, cause: "did not connect"},
		{name: "staged-file-wrong-hash", role: protocol.UpdateMachineRoleHome, reason: protocol.UpdateFailureReasonRefused, cause: "sha256",
			staged: func(t *testing.T, w *appMachineWorld) {
				staged := filepath.Join(filepath.Dir(w.ssh.InstallPath(w.home)), ".attn-pty-host-install")
				data := homeFileBytes(t, staged)
				data[len(data)/2] ^= 0xff
				if err := fakeagent.WriteExecutable(staged, data, 0o755); err != nil {
					t.Fatal(err)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driveHomeUpdateFailure(t, tc)
		})
	}
}

func driveHomeUpdateFailure(t *testing.T, tc homeUpdateFailure) {
	base := testworld.AttnBinary(t)
	w := newAppMachineWorld(t, base)
	installed := w.ssh.InstallPath(w.home)
	before := homeFileBytes(t, installed)
	answering := updateStatusOf(t, w.home)
	next := testworld.AttnUpdateBinary(t, "2.0.0-"+tc.name, "source", "")
	switch tc.name {
	case "release-download-failure":
		next = testworld.AttnUpdateBinary(t, "2.0.1", "release", releaseOf(t, "2.0.0", &next))
	case "compile-failure":
		next = testworld.AttnSourceBinaryIn(t, "2.0.0-compile-failure", checkoutThatFailsToCompile(t, "2.0.0-compile-failure"))
	}
	w.laptop.Stop()
	sends := next
	if tc.arrange != nil {
		sends = tc.arrange(t, w, next)
	}
	var staging, staged, switched *testworld.Pause
	if tc.fail != nil {
		staging = w.laptop.PauseAt(pausepoint.HomeUpdateStaging)
	}
	if tc.staged != nil {
		staged = w.laptop.PauseAt(pausepoint.HomeUpdateStaged)
	}
	if tc.reason == protocol.UpdateFailureReasonNotBack {
		switched = w.laptop.PauseAt(pausepoint.HomeUpdateSwitched)
	}
	w.laptop.RunFrom(next)
	if sends != "" && tc.name != "release-download-failure" && tc.name != "compile-failure" {
		w.laptop.InstallsOnOutposts(sends, "")
	}
	finished := w.laptop.PassesOf(pausepoint.UpdateRunFinished)
	w.laptop.Start()

	if staging != nil {
		resume := staging.Take()
		tc.fail(t, w)
		resume()
	}
	if staged != nil {
		resume := staged.Take()
		tc.staged(t, w)
		resume()
	}
	if switched != nil {
		resume := switched.Take()
		w.ssh.Fail("home", "ssh: connect to host home port 22: Connection refused")
		resume()
		w.laptop.AwaitTimer(daemon.HomeRejoinTimer)
		w.laptop.FirePending(daemon.HomeRejoinTimer)
	}
	run := finishedRunOf(t, w.laptop, finished)
	row := machineRowOf(t, run, tc.role)
	if tc.reason == protocol.UpdateFailureReasonNotBack && !strings.Contains(protocol.Deref(row.Build), "2.0.0-"+tc.name) {
		t.Fatalf("a home that switched and did not come back names build %q, not the one it installed", protocol.Deref(row.Build))
	}
	if run.Phase != protocol.UpdatePhaseFailed || protocol.Deref(row.Reason) != tc.reason || protocol.Deref(row.Action) != "Update" || !strings.Contains(protocol.Deref(row.Detail), tc.cause) {
		for _, r := range run.Outposts {
			t.Logf("row %s %s: %s (%s) %s", protocol.Deref(r.Role), r.State, protocol.Deref(r.Detail), protocol.Deref(r.Reason), protocol.Deref(r.Action))
		}
		t.Fatalf("run %s: %s", run.Phase, protocol.Deref(run.Outcome))
	}
	status := w.laptop.Attn("update", "status")
	requireStdout(t, status, "("+strings.ReplaceAll(string(tc.reason), "_", " ")+")", "; Update")
	t.Logf("%s", strings.TrimSpace(status.Stdout))
	if tc.reason != protocol.UpdateFailureReasonNotBack {
		if !bytes.Equal(homeFileBytes(t, installed), before) {
			t.Fatal("the failed update changed the home's attn")
		}
		if now := updateStatusOf(t, w.home); now.Version != answering.Version || now.Commit != answering.Commit {
			t.Fatalf("the home answers on %s %s, not its old build %s %s", now.Version, now.Commit, answering.Version, answering.Commit)
		}
	}
	if tc.reason == protocol.UpdateFailureReasonUnreachable {
		w.home.Stop()
		requireStdout(t, w.laptop.Attn("update", "status"), "(unreachable)")
	}
}

func checkoutThatFailsToCompile(t *testing.T, tag string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("build:\n\t@echo 'cmd/attnd/main.go:1:1: expected package'; exit 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "Makefile"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "a build that does not compile"}, {"tag", tag}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo
}

func shellPid(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestAHomeUpdateCutByARestartOfTheAppMachineGivesWayToWhatTheHomeRunsWhenItAnswers(t *testing.T) {
	cut := func(t *testing.T, at string) (*appMachineWorld, string, <-chan struct{}, <-chan struct{}) {
		w := newAppMachineWorld(t, testworld.AttnBinary(t))
		next := testworld.AttnUpdateBinary(t, "2.0.0-cut-at-"+at, "source", "")
		w.laptop.Stop()
		w.laptop.RunFrom(next)
		w.laptop.InstallsOnOutposts(next, "")
		pause := w.laptop.PauseAt(at)
		w.laptop.Start()
		resume := pause.Take()
		w.laptop.Stop()
		resume()
		w.ssh.Fail("home", "ssh: connect to host home port 22: Connection refused")
		finished := w.laptop.PassesOf(pausepoint.UpdateRunFinished)
		bridged := w.laptop.PassesOf(pausepoint.HomeBridged)
		w.laptop.Start()
		run := finishedRunOf(t, w.laptop, finished)
		home := machineRowOf(t, run, protocol.UpdateMachineRoleHome)
		if run.Phase != protocol.UpdatePhaseFailed || protocol.Deref(home.Reason) != protocol.UpdateFailureReasonInterrupted || home.Action != nil {
			t.Fatalf("the run a restart cut: %+v, home row %+v", run, home)
		}
		requireStdout(t, w.laptop.Attn("update", "status"), "(interrupted)", "once the home answers")
		w.ssh.Restore("home")
		return w, next, finished, bridged
	}

	t.Run("before-the-switch", func(t *testing.T) {
		w, _, finished, _ := cut(t, pausepoint.HomeUpdateStaging)
		stop := w.laptop.FireTimers(bridge.RetryTimer)
		run := finishedRunOf(t, w.laptop, finished)
		stop()
		app := machineRowOf(t, run, protocol.UpdateMachineRoleAppMachine)
		if home := machineRowOf(t, run, protocol.UpdateMachineRoleHome); home.Reason != nil || app.State != protocol.UpdateOutpostStateSkipped || protocol.Deref(app.Action) != "Update" {
			t.Fatalf("the home's answer on its old build shows %+v %+v, not the build difference with Update", home, app)
		}
	})

	t.Run("after-the-switch", func(t *testing.T) {
		w, next, _, bridged := cut(t, pausepoint.HomeUpdateSwitched)
		stop := w.laptop.FireTimers(bridge.RetryTimer)
		w.home.AwaitDaemonReplaced()
		<-bridged
		stop()
		if status := updateStatusOf(t, w.laptop); !strings.Contains(status.Version, "2.0.0-cut-at-") || status.Run != nil && status.Run.Phase == protocol.UpdatePhaseFailed {
			t.Fatalf("attn update status on the app machine, with its home back on the new build: %+v", status)
		}
		if shown := w.laptop.MachineApp().Initial.UpdateRun; shown != nil {
			t.Fatalf("the app reads the settled run %+v from the app machine while the home answers", shown)
		}
		requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), next)
	})
}

func TestUpdateFromAnAppMachineThatRestartedAfterItsHomeDidNotComeBackMovesTheHome(t *testing.T) {
	w := newAppMachineWorld(t, testworld.AttnBinary(t))
	next := testworld.AttnUpdateBinary(t, "2.0.0-update-after-restart", "source", "")
	w.laptop.Stop()
	w.laptop.RunFrom(next)
	w.laptop.InstallsOnOutposts(next, "")
	switched := w.laptop.PauseAt(pausepoint.HomeUpdateSwitched)
	finished := w.laptop.PassesOf(pausepoint.UpdateRunFinished)
	w.laptop.Start()
	resume := switched.Take()
	w.ssh.Fail("home", "ssh: connect to host home port 22: Connection refused")
	resume()
	w.laptop.AwaitTimer(daemon.HomeRejoinTimer)
	w.laptop.FirePending(daemon.HomeRejoinTimer)
	if home := machineRowOf(t, finishedRunOf(t, w.laptop, finished), protocol.UpdateMachineRoleHome); protocol.Deref(home.Reason) != protocol.UpdateFailureReasonNotBack || protocol.Deref(home.Action) != "Update" {
		t.Fatalf("the home that did not come back: %+v", home)
	}

	w.laptop.Stop()
	w.laptop.Start()
	w.laptop.AwaitTimer(bridge.RetryTimer)
	app := w.laptop.MachineApp()
	if shown := app.Initial.UpdateRun; shown == nil || protocol.Deref(machineRowOf(t, *shown, protocol.UpdateMachineRoleHome).Reason) != protocol.UpdateFailureReasonNotBack {
		t.Fatalf("after its restart the app machine shows %+v, not the home that did not come back", shown)
	}
	w.ssh.Restore("home")
	app.Send(protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate, RequestID: protocol.Ptr("update")})
	if started := testworld.Await(app, protocol.EventStartUpdateResult, func(protocol.StartUpdateResultMessage) bool { return true }); !started.Success {
		t.Fatalf("Update after the restart: %+v", started)
	}
	switched.Take()()
	stop := w.laptop.FireTimers(bridge.RetryTimer)
	w.home.AwaitDaemonReplaced()
	fleet := homeRunOf(t, w.home.App(), "fleet")
	stop()
	if home := machineRowOf(t, fleet, protocol.UpdateMachineRoleHome); fleet.Phase != protocol.UpdatePhaseDone || !strings.Contains(protocol.Deref(home.Build), "2.0.0-update-after-restart") {
		t.Fatalf("the home after Update from the restarted app machine: %s %s, home %+v", fleet.Phase, protocol.Deref(fleet.Outcome), home)
	}
	requireSameFile(t, "the home's attn", w.ssh.InstallPath(w.home), next)
}

func TestAnOutpostThatSwitchedAndDidNotComeBackNamesTheBuildItSwitchedToAsUnconfirmed(t *testing.T) {
	base := testworld.AttnBinary(t)
	var switched *testworld.Pause
	w := newAppMachineWorld(t, base, func(w *appMachineWorld) { switched = w.home.PauseAt(pausepoint.OutpostUpdateSwitched) })
	w.addBox(base)
	next := testworld.AttnUpdateBinary(t, "2.0.0-outpost-not-back", "source", "")
	w.installOnLaptop(next)
	resume := switched.Take()
	w.home.AwaitDaemonReplaced()
	w.ssh.Fail("box", "ssh: connect to host box port 22: Connection refused")
	resume()
	w.home.AwaitTimer(daemon.OutpostRejoinTimer)
	w.home.FirePending(daemon.OutpostRejoinTimer)
	fleet := homeRunOf(t, w.home.App(), "fleet")
	row := machineRowOf(t, fleet, protocol.UpdateMachineRoleOutpost)
	if row.State != protocol.UpdateOutpostStateFailed || protocol.Deref(row.Reason) != protocol.UpdateFailureReasonNotBack || !strings.Contains(protocol.Deref(row.Build), "2.0.0-outpost-not-back") || protocol.Deref(row.Action) != "Update outpost" {
		t.Fatalf("the outpost that switched and did not come back: %+v (%s)", row, protocol.Deref(row.Detail))
	}
	status := w.home.Attn("update", "status")
	requireStdout(t, status, "(not back)", "unconfirmed", "; Update outpost")
	t.Log(strings.TrimSpace(status.Stdout))
}
