package main_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
	"nhooyr.io/websocket"
)

func writeUpgradeAgentFixture(t *testing.T, root string) string {
	t.Helper()
	fixture := filepath.Join(root, "fixture-agent")
	script := `#!/bin/sh
set -eu
stty -echo
terminal_id="${ATTN_TERMINAL_ID:-${ATTN_SESSION_ID:-}}"
conversation="native-$terminal_id"
previous=''
for arg in "$@"; do
  if [ "$previous" = --session-id ]; then conversation="$arg"; fi
  previous="$arg"
done
transcript="$ATTN_TOOL_HOME/.codex/sessions/$conversation.jsonl"
mkdir -p "$ATTN_TOOL_HOME/.codex/sessions"
printf '{"type":"session_meta","payload":{"id":"%s","cwd":"%s"}}\n' "$conversation" "$PWD" > "$transcript"
printf '{"session_id":"%s","transcript_path":"%s"}' "$conversation" "$transcript" | "$ATTN_WRAPPER_PATH" _hook-session-start
while IFS= read -r line; do
  printf '__ACK_%s_%s_%s__\n' "$terminal_id" "$line" "$$"
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "dscl"), []byte("#!/bin/sh\nprintf 'UserShell: /bin/sh\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestSessionsSurviveDaemonRestartsAndSharedHostGenerations(t *testing.T) {
	hostBinary := os.Getenv("ATTN_TEST_PTY_HOST")
	if hostBinary == "" {
		t.Skip("ATTN_TEST_PTY_HOST is unset: run it through scripts/test-go.sh")
	}
	root, err := os.MkdirTemp("/tmp", "attn-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	newBinary := testworld.AttnBinary(t)
	fixture := writeUpgradeAgentFixture(t, root)
	var workers = make(map[int]bool)
	agentPIDs := make(map[string]int)
	terminals := make(map[string]string)
	t.Cleanup(func() {
		for pid := range workers {
			process, _ := os.FindProcess(pid)
			_ = process.Kill()
		}
	})
	start := func(binary, host string) *upgradeDaemon {
		d := startUpgradeDaemon(t, root, binary, host)
		d.workers = workers
		d.agentPIDs = agentPIDs
		d.terminals = terminals
		return d
	}
	current := start(newBinary, hostBinary)
	current.spawn("shell", "shell", "")
	current.spawn("agent", "claude", fixture)
	current.spawn("promote-agent", "claude", fixture)
	identities := map[string]ptyworker.RegistryEntry{}
	for _, id := range []string{"shell", "agent", "promote-agent"} {
		identities[id] = current.identity(id)
		current.probe(id, identities[id].ChildPID, "first-daemon")
	}
	current.command(map[string]any{"cmd": "reload_session", "id": "promote-agent", "cols": 80, "rows": 24}, "reload_session_result", "promote-agent")
	promoted := current.identity("promote-agent")
	if promoted.ChildPID == identities["promote-agent"].ChildPID || promoted.WorkerPID != identities["agent"].WorkerPID {
		t.Fatalf("reload did not replace only the selected child on the shared host: before=%+v after=%+v", identities["promote-agent"], promoted)
	}
	identities["promote-agent"] = promoted
	priorAgentPID := agentPIDs["promote-agent"]
	delete(agentPIDs, "promote-agent")
	current.probe("promote-agent", promoted.ChildPID, "promoted")
	if agentPIDs["promote-agent"] == priorAgentPID {
		t.Fatalf("reload kept the agent process %d, want a new one", priorAgentPID)
	}
	assertAll := func(d *upgradeDaemon, phase string) {
		for _, id := range slices.Sorted(maps.Keys(identities)) {
			d.assertIdentity(id, identities[id])
			d.probe(id, identities[id].ChildPID, phase)
		}
	}
	assertAll(current, "after-reload")
	current.stop()

	if err := os.RemoveAll(ptyhost.ArtifactsDir(root, current.instanceID)); err != nil {
		t.Fatal(err)
	}
	current = start(newBinary, hostBinary)
	assertAll(current, "mixed-restart")
	current.stop()

	nextHost := filepath.Join(root, "next-pty-host")
	if err := os.WriteFile(nextHost, []byte("#!/bin/sh\nexec '"+strings.ReplaceAll(hostBinary, "'", "'\\''")+"' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	current = start(newBinary, nextHost)
	assertAll(current, "host-upgrade")
	current.waitForLog("shared PTY host artifact promoted", nil)
	current.spawn("next-agent", "claude", fixture)
	next := current.identity("next-agent")
	identities["next-agent"] = next
	if next.WorkerPID == identities["agent"].WorkerPID {
		t.Fatal("new host generation reused the old host")
	}
	current.probe("next-agent", next.ChildPID, "next-host")
	assertAll(current, "all-generations-alive")
	current.command(map[string]any{"cmd": "pty_resize", "id": terminals["shell"], "cols": 97, "rows": 31}, "pty_resized", terminals["shell"])
	current.probe("shell", identities["shell"].ChildPID, "resized")
	current.stop()

	brokenHost := filepath.Join(root, "broken-pty-host")
	if err := os.WriteFile(brokenHost, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	current = start(newBinary, brokenHost)
	current.waitForLog("failed validation", nil)
	if got := current.notificationCount("pty_host_rejected"); got != 1 {
		t.Fatalf("rejection notifications = %d, want exactly one", got)
	}
	assertAll(current, "rejected-candidate")
	current.spawn("last-known-good-agent", "claude", fixture)
	lastKnownGood := current.identity("last-known-good-agent")
	identities["last-known-good-agent"] = lastKnownGood
	if lastKnownGood.WorkerPID != next.WorkerPID {
		t.Fatalf("launch after a rejected candidate used host %d, want last-known-good host %d", lastKnownGood.WorkerPID, next.WorkerPID)
	}
	current.probe("last-known-good-agent", lastKnownGood.ChildPID, "last-known-good")
	current.stop()

	current = start(newBinary, brokenHost)
	assertAll(current, "unchanged-rejected-candidate")
	if got := current.notificationCount("pty_host_rejected"); got != 1 {
		t.Fatalf("restart rechecked an unchanged rejected build: %d rejection notifications", got)
	}
	current.stop()

	current = start(newBinary, filepath.Join(root, "missing-host"))
	assertAll(current, "missing-bundle-restart")
	current.spawn("missing-bundle-agent", "claude", fixture)
	identities["missing-bundle-agent"] = current.identity("missing-bundle-agent")
	if identities["missing-bundle-agent"].WorkerPID != next.WorkerPID {
		t.Fatal("a missing bundle host moved new launches off the last-known-good host")
	}
	assertAll(current, "missing-bundle-new-agent")
	for _, id := range slices.Sorted(maps.Keys(identities)) {
		current.command(map[string]any{"cmd": "unregister", "id": id}, "session_unregistered", "")
	}
	current.stop()
}

func TestAnUpdateFromTheReleaseKeepsLiveAgentsAndPutsNewOnesOnTheHostWithDirectLaunch(t *testing.T) {
	for _, path := range []struct {
		name    string
		appOpen bool
	}{
		{name: "the app runs while it is replaced", appOpen: true},
		{name: "the app is quit before it is replaced", appOpen: false},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()
			u := startTheRelease(t)
			app := u.ReleaseApp()
			turnOnTheSharedHost(t, app)
			claude, codex := u.spawnAgent(app, fakeagent.Claude), u.spawnAgent(app, fakeagent.Codex)
			shell := u.spawnShell(app)
			parents := map[string]string{}
			for _, agent := range []releaseAgent{claude, codex} {
				parents[agent.session] = u.parentPID(agent.run)
				u.turn(app, agent, "before-update")
			}

			working := u.startTurn(app, claude, "turn-across-the-replace")
			if !path.appOpen {
				app.Close()
			}
			u.InstallBuild(u.currentAttn, u.currentHost)
			claude.run.Reply("turn-across-the-replace reply <!-- attn:state=idle -->")
			stopped := func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle }
			if path.appOpen {
				testworld.AwaitStateAfter(app, working, stopped)
			}

			app = u.ensureCurrent()
			if len(app.Initial.Warnings) != 0 {
				t.Errorf("the updated daemon warns: %+v", app.Initial.Warnings)
			}
			if !stopped(initialSession(t, app, claude.session)) {
				testworld.AwaitStateAfter(app, working, stopped)
			}
			app.AwaitScreen(claude.session, "turn-across-the-replace reply")
			for _, agent := range []releaseAgent{claude, codex} {
				u.assertNoReloadNotice(app, agent.session)
				app.AwaitScreen(agent.session, "before-update reply")
				u.assertSameParent(agent, parents[agent.session])
				u.turn(app, agent, "after-update")
			}
			u.assertNoReloadNotice(app, shell.session)
			shell.assertSameProcess(app)

			for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
				agent := u.spawnAgent(app, h)
				u.assertParent(agent.run, "attn-pty-host")
				u.turn(app, agent, "new-after-update")
			}
			for _, agent := range []releaseAgent{claude, codex} {
				u.assertSameParent(agent, parents[agent.session])
				u.turn(app, agent, "after-new-defaults")
			}
			shell.assertSameProcess(app)
			settled := u.App()
			for _, session := range []string{claude.session, codex.session, shell.session} {
				u.assertNoReloadNotice(settled, session)
			}
		})
	}
}

func TestAnUpdateFromTheReleaseKeepsHostTerminalsAndReloadsThemWithDirectLaunch(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	app := u.ReleaseApp()
	turnOnTheSharedHost(t, app)
	onHost := u.spawnAgent(app, fakeagent.Claude)
	sharedHostOf(t, u.Dir, app.Terminal(onHost.session))
	shellOnHost := u.spawnShell(app)
	hostTerminal := app.Terminal(shellOnHost.session)
	hostBefore := sharedHostOf(t, u.Dir, hostTerminal)
	formatBefore := attachedFormat(app, hostTerminal)
	u.turn(app, onHost, "before-update")

	u.InstallBuild(testworld.AttnBinaryWithSnapshotFormat(t, upgradeMovedSnapshotFormat), ptyhosttest.Variant(t, upgradeMovedSnapshotFormat))
	app = u.ensureCurrent()
	format := attachedFormatAfterMove(app, hostTerminal, formatBefore)
	if format == formatBefore || format == "" {
		t.Fatalf("the host terminal attaches with format %q after the update from %q, want the current build's", format, formatBefore)
	}
	t.Logf("snapshot format moved from %s to %s", formatBefore, format)
	if host := sharedHostOf(t, u.Dir, hostTerminal); host != hostBefore {
		t.Errorf("the terminal moved from host pid %d to %d, want the same process carrying it", hostBefore, host)
	}
	app.AwaitScreen(onHost.session, "before-update reply")
	u.turn(app, onHost, "after-update")
	shellOnHost.assertSameProcess(app)
	settled := u.App()
	for _, session := range []string{onHost.session, shellOnHost.session} {
		u.assertNoReloadNotice(settled, session)
	}

	u.reloadOntoTheHost(app, onHost)
	shellOnHost.assertSameProcess(app)
}

func TestAnUpdateFromTheReleaseStopsShowingAWorkerTerminalAndLeavesItsWorkerRunning(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	app := u.ReleaseApp()
	setSetting(t, app, upgradeSharedHostEnabled, "false")
	onWorker := u.spawnShell(app)
	terminal := app.Terminal(onWorker.session)
	u.assertNoSharedHost(terminal)
	registry, worker := u.workerOf(terminal)
	before, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}

	u.InstallBuild(u.currentAttn, u.currentHost)
	app = u.ensureCurrent()
	for _, warning := range app.Initial.Warnings {
		if warning.Code != "stale_session_missing_worker" {
			t.Errorf("the updated daemon warns: %+v", warning)
		}
	}
	if came := initialSession(t, app, onWorker.session); came.State != protocol.SessionStateRecoverable {
		t.Errorf("the session whose terminal stayed in a worker came back %s, want recoverable", came.State)
	}
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if attached.Success {
		t.Errorf("the updated daemon attached the worker's terminal: %+v", attached)
	}
	if !procreap.ProcessAlive(worker) {
		t.Errorf("the update ended worker %d, want it left running", worker)
	}
	if after, err := os.ReadFile(registry); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the update changed the worker's registry %s (err=%v)", registry, err)
	}
}

func TestAnUpdateFromTheReleaseWhoseNewHostFailsItsCheckShowsTheNoticeAndRefusesNewTerminals(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	app := u.ReleaseApp()
	turnOnTheSharedHost(t, app)
	shell := u.spawnShell(app)
	sharedHostOf(t, u.Dir, app.Terminal(shell.session))

	broken := filepath.Join(u.Dir, "broken-host")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u.InstallBuild(testworld.AttnBinaryWithSnapshotFormat(t, upgradeMovedSnapshotFormat), broken)
	app = u.ensureCurrent()
	if len(u.notifications(app)) == 0 {
		testworld.Await(app, protocol.EventNotificationsUpdated, func(protocol.NotificationsUpdatedMessage) bool { return true })
	}
	if notifications := u.notifications(app); len(notifications) != 1 || notifications[0].Kind != "pty_host_rejected" {
		t.Errorf("notifications = %+v, want one pty_host_rejected", notifications)
	}
	shell.assertSameProcess(app)
	if came := initialSession(t, u.App(), shell.session); !protocol.Deref(came.TerminalBuildStale) {
		t.Errorf("the live shell on the old host shows no reload notice")
	}
	u.assertSpawnRefused(app, "Reinstall attn")
}

func TestAFreshInstallFindsItsHostAndStartsAgentsOnItWithDirectLaunch(t *testing.T) {
	t.Parallel()
	s, currentHost := newInstalledStack(t)
	s.InstallBuild(testworld.AttnBinary(t), currentHost)
	t.Cleanup(func() { s.Attn("daemon", "stop") })
	if got := s.Attn("daemon", "ensure"); got.Code != 0 || !strings.Contains(got.Stdout, `"started"`) {
		t.Fatalf("attn daemon ensure on no data: %+v", got)
	}
	u := &releaseUpdate{Stack: s, t: t}
	app := s.App()
	if len(app.Initial.Warnings) != 0 {
		t.Errorf("a fresh install warns: %+v", app.Initial.Warnings)
	}
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		agent := u.spawnAgent(app, h)
		u.assertParent(agent.run, "attn-pty-host")
		u.turn(app, agent, "on-a-fresh-install")
	}
}

func TestAFreshInstallFindsItsHostAndAnAgentTitledBeforeTheDaemonWatchesLeavesLaunching(t *testing.T) {
	t.Parallel()
	s, currentHost := newInstalledStack(t)
	s.InstallBuild(testworld.AttnBinary(t), currentHost)
	watching := s.PauseAt(pausepoint.PtyHostWatchAll)
	s.Start()
	u := &releaseUpdate{Stack: s, t: t}
	app := s.App()
	agent := u.spawnAgent(app, fakeagent.Codex)
	watching.Await()
	agent.run.Stream("printed after its title")
	app.AwaitScreen(agent.session, "printed after its title")
	watching.Release()
	u.turn(app, agent, "titled-before-the-watch")
}

func TestATerminalOpenedBeforeTheFirstHostCheckFinishesStartsOnTheHost(t *testing.T) {
	t.Parallel()
	s, currentHost := newInstalledStack(t)
	s.InstallBuild(testworld.AttnBinary(t), currentHost)
	t.Cleanup(func() { s.Attn("daemon", "stop") })
	if err := os.RemoveAll(filepath.Join(s.Dir, "pty-hosts")); err != nil {
		t.Fatal(err)
	}
	if got := s.Attn("daemon", "ensure"); got.Code != 0 {
		t.Fatalf("attn daemon ensure with no host build checked yet: %+v", got)
	}
	u := &releaseUpdate{Stack: s, t: t}
	app := s.App()
	if len(app.Initial.Warnings) != 0 {
		t.Errorf("the first start warns: %+v", app.Initial.Warnings)
	}
	for _, phase := range []string{"opened-during-the-check", "opened-after-the-check"} {
		agent := u.spawnAgent(app, fakeagent.Claude)
		u.assertParent(agent.run, "attn-pty-host")
		u.turn(app, agent, phase)
	}
}

func TestAnUpdateFromTheReleaseDropsStoredOffSettingsAndMovesAgentsOntoTheHostWithoutANag(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	app := u.ReleaseApp()
	turnOnTheSharedHost(t, app)
	live := u.spawnAgent(app, fakeagent.Claude)
	u.turn(app, live, "before-update")
	setSetting(t, app, upgradeSharedHostEnabled, "false")
	setSetting(t, app, upgradeDirectLaunchEnabled, "false")

	u.InstallBuild(u.currentAttn, u.currentHost)
	app = u.ensureCurrent()
	if got, stored := app.Initial.Settings[upgradeSharedHostEnabled]; stored {
		t.Errorf("%s = %q after the update, want no such setting", upgradeSharedHostEnabled, got)
	}
	if got, stored := app.Initial.Settings[upgradeDirectLaunchEnabled]; stored {
		t.Errorf("%s = %q after the update, want the key gone", upgradeDirectLaunchEnabled, got)
	}
	if len(app.Initial.Warnings) != 0 {
		t.Errorf("the updated daemon warns: %+v", app.Initial.Warnings)
	}
	u.turn(app, live, "after-update")
	fresh := u.spawnAgent(app, fakeagent.Claude)
	u.assertParent(fresh.run, "attn-pty-host")
	result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(live.session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == live.session })
	if !result.Success {
		t.Fatalf("reload: %s", protocol.Deref(result.Error))
	}
	reloaded := releaseAgent{session: live.session, run: u.Launched(live.session)}
	u.assertParent(reloaded.run, "attn-pty-host")
	u.turn(app, reloaded, "after-reload")
	if notifications := u.notifications(app); len(notifications) != 0 {
		t.Errorf("the update notified: %+v", notifications)
	}
}

func TestAfterAnUpdateARebootBringsSessionsBackOnTheHostWithDirectLaunchAndTheirConversations(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	app := u.ReleaseApp()
	turnOnTheSharedHost(t, app)
	fromRelease := u.spawnAgent(app, fakeagent.Claude)
	u.turn(app, fromRelease, "before-update")
	u.InstallBuild(u.currentAttn, u.currentHost)
	app = u.ensureCurrent()
	onHost := u.spawnAgent(app, fakeagent.Codex)
	u.turn(app, onHost, "on-the-host")

	if got := u.Attn("daemon", "stop"); got.Code != 0 {
		t.Fatalf("attn daemon stop: %+v", got)
	}
	u.Reboot()
	if got := u.Attn("daemon", "ensure"); got.Code != 0 || !strings.Contains(got.Stdout, `"started"`) {
		t.Fatalf("attn daemon ensure after the reboot: %+v", got)
	}
	app = u.App()
	for _, agent := range []releaseAgent{fromRelease, onHost} {
		back := releaseAgent{session: agent.session, run: u.Launched(agent.session)}
		u.assertBackInItsConversation(app, back, agent.run.ConversationID)
	}
}

func TestAHostThatCannotRunRefusesNewTerminalsWithOneNotification(t *testing.T) {
	t.Parallel()
	u := startTheRelease(t)
	u.InstallBuild(u.currentAttn, u.currentHost)
	u.ensureCurrent()
	if got := u.Attn("daemon", "stop"); got.Code != 0 {
		t.Fatalf("attn daemon stop: %+v", got)
	}
	broken := filepath.Join(u.Dir, "broken-host")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u.InstallBuild(u.currentAttn, broken)
	if err := os.RemoveAll(filepath.Join(u.Dir, "pty-hosts")); err != nil {
		t.Fatal(err)
	}
	if got := u.Attn("daemon", "ensure"); got.Code != 0 {
		t.Fatalf("attn daemon ensure with a host that cannot run: %+v", got)
	}
	app := u.App()
	if len(u.notifications(app)) == 0 {
		testworld.Await(app, protocol.EventNotificationsUpdated, func(protocol.NotificationsUpdatedMessage) bool { return true })
	}
	notifications := u.notifications(app)
	if len(notifications) != 1 || notifications[0].Kind != "pty_host_rejected" {
		t.Fatalf("notifications = %+v, want one pty_host_rejected", notifications)
	}
	if !strings.Contains(notifications[0].Impact, "Reinstall attn") {
		t.Errorf("the notification's impact %q does not say how to get the host", notifications[0].Impact)
	}
	u.assertSpawnRefused(app, "Reinstall attn")
}

func TestAMachineWithOnlyAttnRefusesNewTerminalsWithTheRemedyAfterAnUpdate(t *testing.T) {
	t.Parallel()
	skipUnlessAMachineCanHaveNoHost(t)
	releaseAttn := os.Getenv("ATTN_UPGRADE_RELEASE_BIN")
	if releaseAttn == "" {
		t.Skip("run scripts/test-pty-upgrade.sh to start from the real previous release")
	}
	s, _ := newInstalledStack(t)
	s.Vars = append(s.Vars, "XDG_DATA_HOME="+filepath.Join(s.Dir, "share"))
	u := &releaseUpdate{Stack: s, t: t, currentAttn: testworld.AttnBinary(t)}
	skipIfTheReleaseFindsAHostOfThisMachine(t)
	s.InstallBuild(releaseAttn, "")
	s.StartRelease()

	s.InstallBuild(u.currentAttn, "")
	app := u.ensureCurrent()
	if len(app.Initial.Warnings) != 0 {
		t.Errorf("the updated daemon warns: %+v", app.Initial.Warnings)
	}
	u.assertSpawnRefused(app, "Reinstall attn")
}

func TestDirectLaunchStartsAgentsThroughTheUsersLoginShell(t *testing.T) {
	loginShell := func(t *testing.T, s *testworld.Stack, body string) string {
		t.Helper()
		path := filepath.Join(s.Dir, "login-shell.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n[ \"$1\" = -l ] && shift\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Run("an rc file that prints text", func(t *testing.T) {
		t.Parallel()
		u := startDirectLaunch(t, func(s *testworld.Stack) string { return loginShell(t, s, "printf 'welcome from the rc file\\n'") })
		app := u.App()
		agent := u.spawnAgent(app, fakeagent.Claude)
		u.assertParent(agent.run, "attn-pty-host")
		u.turn(app, agent, "after-a-chatty-rc")
	})
	t.Run("an rc file that takes its time", func(t *testing.T) {
		t.Parallel()
		var held string
		u := startDirectLaunch(t, func(s *testworld.Stack) string {
			held = filepath.Join(s.Dir, "rc-held")
			if err := syscall.Mkfifo(held, 0o600); err != nil {
				t.Fatal(err)
			}
			return loginShell(t, s, `case "$*" in *attn-agent*) read _ < `+held+`;; esac`)
		})
		app := u.App()
		session := u.Spawn(app, fakeagent.Claude, u.Path("claude"))
		release, err := os.OpenFile(held, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := release.WriteString("go\n"); err != nil {
			t.Fatal(err)
		}
		_ = release.Close()
		agent := releaseAgent{session: session, run: u.Launched(session)}
		u.assertParent(agent.run, "attn-pty-host")
		u.turn(app, agent, "after-a-slow-rc")
	})
	t.Run("the login shell is fish", func(t *testing.T) {
		t.Parallel()
		fish, err := exec.LookPath("fish")
		if err != nil {
			t.Skip("fish is not installed; scripts/test-pty-upgrade.sh requires it")
		}
		u := startDirectLaunch(t, func(s *testworld.Stack) string {
			s.Vars = append(s.Vars, "XDG_CONFIG_HOME="+filepath.Join(s.Dir, "config"))
			return fish
		})
		app := u.App()
		for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
			agent := u.spawnAgent(app, h)
			u.assertParent(agent.run, "attn-pty-host")
			u.turn(app, agent, "under-fish")
		}
	})
	t.Run("the harness is missing", func(t *testing.T) {
		t.Parallel()
		u := startDirectLaunch(t, func(s *testworld.Stack) string {
			s.Vars = append(s.Vars, "ATTN_CLAUDE_EXECUTABLE="+filepath.Join(s.Dir, "no-claude"))
			return ""
		})
		app := u.App()
		result, _, _ := u.RequestSpawn(app, fakeagent.Claude, u.Path("claude"))
		if result.Success || !strings.Contains(protocol.Deref(result.Error), `agent "claude" is not available`) {
			t.Errorf("starting an agent with no harness: success=%t error=%q, want a refusal that names the harness", result.Success, protocol.Deref(result.Error))
		}
	})
	t.Run("an rc file that exits", func(t *testing.T) {
		t.Parallel()
		u := startDirectLaunch(t, func(s *testworld.Stack) string {
			return loginShell(t, s, `case "$*" in *attn-agent*) echo 'rc: broken config' >&2; exit 7;; esac`)
		})
		app := u.App()
		u.assertStopsShowing(app, u.Spawn(app, fakeagent.Claude, u.Path("claude")), "rc: broken config")
	})
	t.Run("the harness cannot run", func(t *testing.T) {
		t.Parallel()
		const name = "claude-cannot-run"
		u := startDirectLaunch(t, func(s *testworld.Stack) string {
			path := filepath.Join(s.Dir, name)
			if err := os.WriteFile(path, []byte("#!/no/such/interpreter\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			s.Vars = append(s.Vars, "ATTN_CLAUDE_EXECUTABLE="+path)
			return ""
		})
		app := u.App()
		u.assertStopsShowing(app, u.Spawn(app, fakeagent.Claude, u.Path("claude")), name)
	})
}

func TestAPhoneReattachesToAHostTerminalAndDrawsAfterAnUpdate(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "current"), "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
	u := &releaseUpdate{Stack: s, t: t}
	app := s.App()
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	qr := testworld.ParsePhoneQR(t, beginPairing(t, app, app.SelectedProfile(), "pair").Qr)
	socket, err := testworld.NewPhone(t, cloud, "Test iPhone").DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	shell := u.spawnShell(app)
	terminal := app.Terminal(shell.session)
	sharedHostOf(t, s.Dir, terminal)

	s.Stop()
	socket.AwaitPresence(false)
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "next"), "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "next"))
	socket.AwaitPresence(true)
	if err := socket.Resume(qr); err != nil {
		t.Fatal(err)
	}
	socket.Send(homegateway.HomeGatewayHello{Cmd: "hello", API: homegateway.APIVersion, DeviceID: "phone"})
	socket.Send(homegateway.TerminalAttach{Cmd: "terminal_attach", TerminalID: terminal, Cols: 50, Rows: 30, Credit: 256 << 10})
	for {
		raw, err := socket.Next()
		if err != nil {
			t.Fatalf("waiting for the terminal's screen: %v", err)
		}
		var head struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(raw, &head)
		if head.Event == "terminal_error" {
			t.Fatalf("the phone's attach after the update was refused: %s", raw)
		}
		if head.Event == "terminal_screen" {
			var screen homegateway.TerminalScreen
			_ = json.Unmarshal(raw, &screen)
			ansi, _ := base64.StdEncoding.DecodeString(screen.Ansi)
			if !strings.Contains(string(ansi), "printed-2") {
				t.Fatalf("the screen after the update shows %q, want the shell's earlier output", ansi)
			}
			break
		}
	}
	shell.assertSameProcess(s.App())
}

func TestKillingTheDaemonDuringAHostMoveLeavesTerminalsAliveAndTheNextStartRecovers(t *testing.T) {
	for _, point := range []string{"shared-handover-before-request", "shared-handover-after-request"} {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			current := testworld.AttnBinaryWithSnapshotFormat(t, "current")
			next := testworld.AttnBinaryWithSnapshotFormat(t, "next")
			nextHost := "ATTN_PTY_HOST_BINARY=" + ptyhosttest.Variant(t, "next")
			s.StartBinary(current, "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
			u := &releaseUpdate{Stack: s, t: t}
			app := s.App()
			agent := u.spawnAgent(app, fakeagent.Claude)
			u.turn(app, agent, "before-update")
			shell := u.spawnShell(app)
			hostBefore := sharedHostOf(t, s.Dir, app.Terminal(shell.session))
			s.Stop()

			held := s.PauseAt(point)
			s.RunFrom(next)
			s.Vars = append(s.Vars, nextHost, "ATTN_CRASH_AT="+point)
			s.StartHeldAt(held)
			held.Release()
			s.AwaitCrash()

			s.Vars = append(s.Vars, "ATTN_CRASH_AT=")
			s.Start()
			app = s.App()
			terminal := app.Terminal(shell.session)
			attach := func() protocol.AttachResultMessage {
				return testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
					protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
			}
			if attached := attach(); attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "next" {
				testworld.AwaitEvent(app, "the attached stream of "+terminal+" to end when its host hands over", func(e protocol.WebSocketEvent) bool {
					return e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == terminal
				})
				if attached := attach(); attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "next" {
					t.Fatalf("after the recovery the terminal attached with snapshot %+v, want its host on next", attached.Snapshot)
				}
			}
			if host := sharedHostOf(t, s.Dir, terminal); host != hostBefore {
				t.Errorf("the terminal moved from host pid %d to %d, want the same process carrying it", hostBefore, host)
			}
			shell.assertSameProcess(app)
			u.turn(app, agent, "after-recovery")
			u.assertNoReloadNotice(s.App(), shell.session)
		})
	}
}

func TestAnOutpostOnTheReleaseKeepsItsTerminalsWhenTheHomeAddsAndUpdatesIt(t *testing.T) {
	t.Parallel()
	releaseAttn, releaseHost, currentHost := os.Getenv("ATTN_UPGRADE_RELEASE_BIN"), os.Getenv("ATTN_UPGRADE_RELEASE_HOST"), os.Getenv("ATTN_TEST_PTY_HOST")
	if releaseAttn == "" || releaseHost == "" || currentHost == "" || os.Getenv("ATTN_E2E_BIN") == "" {
		t.Skip("run scripts/test-pty-upgrade.sh to start from the real previous release")
	}
	current := testworld.AttnBinary(t)
	ssh := testworld.NewSSH(t)
	remote, home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t)
	remote.Vars = append(remote.Vars, "ATTN_PTY_HOST_BINARY=", "ATTN_PTY_SKIP_STARTUP_PROBE=")
	ssh.Use(home.World)
	ssh.Route("release-outpost", remote)
	ssh.InstallBinary(remote, releaseAttn)
	releaseHostBytes, err := os.ReadFile(releaseHost)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ssh.HostPath(remote), releaseHostBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	remote.RunFrom(ssh.InstallPath(remote))
	remote.Vars = append(remote.Vars, "ATTN_WRAPPER_PATH="+ssh.InstallPath(remote))
	remote.StartRelease()
	remoteApp := remote.ReleaseApp()
	turnOnTheSharedHost(t, remoteApp)
	liveShell := remote.Spawn(remoteApp, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"))
	pidPath := filepath.Join(remote.Dir, "shell.pid")
	remoteApp.TypeLine(liveShell, "echo $$ > "+pidPath+"; echo before-$((1+1))")
	remoteApp.AwaitScreen(liveShell, "before-2")
	before, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}

	home.InstallsOnOutposts(current, currentHost)
	home.Start()
	app := enableOutposts(t, home)
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "release-outpost"})
	row := awaitOutpostInstall(app)
	if row.Update.Phase != protocol.OutpostUpdatePhaseDone || row.State != protocol.OutpostStateConnected {
		t.Fatalf("adding the outpost: %+v %+v", row, row.Update)
	}
	remote.AwaitStopped()
	installDir := filepath.Dir(ssh.InstallPath(remote))
	for name, shipped := range map[string]string{"attn": current, "attnd": filepath.Join(filepath.Dir(current), "attnd"), ptyhost.BinaryName: currentHost} {
		if sha256OfFile(t, filepath.Join(installDir, name)) != sha256OfFile(t, shipped) {
			t.Errorf("the update did not install this build's %s", name)
		}
	}
	host := row.ID
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].Notebook != nil && m.Outposts[0].Notebook.State == protocol.OutpostNotebookStateInSync
	})
	remoteApp = remote.App()
	if ready := remote.Attn("seed", "ready"); ready.Code != 0 {
		t.Errorf("attn seed ready on the updated outpost: %+v", ready)
	}
	if came := initialSession(t, remoteApp, liveShell); protocol.Deref(came.TerminalBuildStale) {
		t.Errorf("the outpost's shell came back with the reload notice")
	}
	terminal := remoteApp.Terminal(liveShell)
	attached := testworld.Request(remoteApp, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	shellPID, err := strconv.Atoi(strings.TrimSpace(string(before)))
	if err != nil {
		t.Fatal(err)
	}
	if !protocol.Deref(attached.Running) || protocol.Deref(attached.Pid) != shellPID {
		t.Errorf("the outpost's shell attached as %+v, want the same running process %d", attached, shellPID)
	}
	remoteApp.AwaitScreen(liveShell, "before-2")
	remoteApp.Type(liveShell, "echo after-$((2+2))\r", "keyboard")
	remoteApp.AwaitScreen(liveShell, "after-4")
	fresh := home.Spawn(app, fakeagent.Claude, remote.Path("claude"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	run := remote.LaunchedCarrying(app.Terminal(fresh))
	app.TypeLine(fresh, "on-the-updated-outpost")
	if prompt := run.Prompted(); prompt != "on-the-updated-outpost" {
		t.Errorf("the new agent on the outpost took %q", prompt)
	}
	inHost := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("in-host"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	app.TypeLine(inHost, `echo "owner=$(`+terminalOwner+`)"`)
	app.AwaitScreen(inHost, "owner="+ptyhost.BinaryName)
}

func TestAnOutpostAddedByAddressOnTheReleaseExplainsItselfAfterTheUpdate(t *testing.T) {
	t.Parallel()
	u := startRelease(t, os.Getenv("ATTN_UPGRADE_ADDRESS_RELEASE_BIN"), os.Getenv("ATTN_UPGRADE_ADDRESS_RELEASE_HOST"))
	app := u.ReleaseApp()
	requireStdout(t, u.Attn("settings", "set", "outposts_enabled", "true"), "true")
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	app.Send(struct {
		Cmd     string `json:"cmd"`
		Name    string `json:"name"`
		Address string `json:"address"`
	}{protocol.CmdAddOutpost, "by-address", "127.0.0.1:1"})
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 1 })

	u.InstallBuild(u.currentAttn, u.currentHost)
	app = u.ensureCurrent()
	row := outpostStateOnConnect(app, "by-address", protocol.OutpostStateRefused)
	if !strings.Contains(protocol.Deref(row.LastError), "add it again by the name you use with ssh") {
		t.Errorf("the refused row does not say how to recover: %q", protocol.Deref(row.LastError))
	}
	app.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: row.ID})
	warning := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool { return protocol.Deref(m.Cmd) == protocol.CmdRemoveOutpost })
	if !strings.Contains(warning.Error, "enrollment leave") {
		t.Errorf("the remove warning does not say how to stop the outpost: %q", warning.Error)
	}
	testworld.Await[protocol.OutpostsUpdatedMessage](app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
}

func skipUnlessAMachineCanHaveNoHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("only Linux publishes attn without its host")
	}
}

func skipIfTheReleaseFindsAHostOfThisMachine(t *testing.T) {
	t.Helper()
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".local", "bin", ptyhost.BinaryName)); err == nil {
			t.Skipf("the release would pin the host in %s/.local/bin", home)
		}
	}
	if path, err := exec.LookPath(ptyhost.BinaryName); err == nil {
		t.Skipf("the release would pin the host on PATH at %s", path)
	}
}

func attachedFormat(app *testworld.Peer, terminal string) string {
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if attached.Snapshot == nil {
		return ""
	}
	return protocol.Deref(attached.Snapshot.Format)
}

// attachedFormatAfterMove waits out a host handover that is still running when the app first attaches.
func attachedFormatAfterMove(app *testworld.Peer, terminal, before string) string {
	if format := attachedFormat(app, terminal); format != before {
		return format
	}
	testworld.AwaitEvent(app, "the attached stream of "+terminal+" to end when its host hands over", func(e protocol.WebSocketEvent) bool {
		return e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == terminal
	})
	return attachedFormat(app, terminal)
}

// releaseUpdate is a stack that started on the real previous release, with the current build ready to install.
type releaseUpdate struct {
	*testworld.Stack
	t                        *testing.T
	currentAttn, currentHost string
}

type releaseAgent struct {
	session string
	run     *fakeagent.Run
}

type releaseShell struct {
	u       *releaseUpdate
	session string
	pid     string
	checks  int
}

func startTheRelease(t *testing.T) *releaseUpdate {
	t.Helper()
	return startRelease(t, os.Getenv("ATTN_UPGRADE_RELEASE_BIN"), os.Getenv("ATTN_UPGRADE_RELEASE_HOST"))
}

func startRelease(t *testing.T, releaseAttn, releaseHost string) *releaseUpdate {
	t.Helper()
	currentHost := os.Getenv("ATTN_TEST_PTY_HOST")
	if releaseAttn == "" || releaseHost == "" || currentHost == "" || os.Getenv("ATTN_E2E_BIN") == "" {
		t.Skip("run scripts/test-pty-upgrade.sh to start from the real previous release")
	}
	s, _ := newInstalledStack(t)
	u := &releaseUpdate{Stack: s, t: t, currentAttn: testworld.AttnBinary(t), currentHost: currentHost}
	s.InstallBuild(releaseAttn, releaseHost)
	s.StartRelease()
	return u
}

// newInstalledStack is a stack whose daemon finds its PTY backend and host the way an installed build does.
func newInstalledStack(t *testing.T) (*testworld.Stack, string) {
	t.Helper()
	currentHost := os.Getenv("ATTN_TEST_PTY_HOST")
	if currentHost == "" || os.Getenv("ATTN_E2E_BIN") == "" {
		t.Skip("run scripts/test-pty-upgrade.sh for the two-binary upgrade tests")
	}
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_PTY_HOST_BINARY=", "ATTN_PTY_SKIP_STARTUP_PROBE=")
	t.Cleanup(func() {
		if t.Failed() {
			s.LogDaemonTail()
		}
	})
	return s, currentHost
}

func startDirectLaunch(t *testing.T, prepare func(*testworld.Stack) (loginShell string)) *releaseUpdate {
	t.Helper()
	s, currentHost := newInstalledStack(t)
	if shell := prepare(s); shell != "" {
		s.Vars = append(s.Vars, "SHELL="+shell)
	}
	s.InstallBuild(testworld.AttnBinary(t), currentHost)
	s.Start()
	return &releaseUpdate{Stack: s, t: t}
}

func (u *releaseUpdate) notifications(app *testworld.Peer) []protocol.Notification {
	u.T.Helper()
	requestID := uuid.NewString()
	listed := testworld.Request(app, protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr(requestID)},
		protocol.EventNotificationListResult, func(r protocol.NotificationListResultMessage) bool { return r.RequestID == requestID })
	return listed.Notifications
}

func (u *releaseUpdate) reloadOntoTheHost(app *testworld.Peer, agent releaseAgent) {
	u.T.Helper()
	conversation := agent.run.ConversationID
	result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(agent.session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == agent.session })
	if !result.Success {
		u.T.Fatalf("reload: %s", protocol.Deref(result.Error))
	}
	u.assertBackInItsConversation(app, releaseAgent{session: agent.session, run: u.Launched(agent.session)}, conversation)
}

func (u *releaseUpdate) assertBackInItsConversation(app *testworld.Peer, agent releaseAgent, conversation string) {
	u.T.Helper()
	u.assertParent(agent.run, "attn-pty-host")
	u.turn(app, agent, "back-again")
	if !agent.run.Resumed || agent.run.ConversationID != conversation {
		u.T.Errorf("session %s ran conversation %q (resumed=%t), want %q resumed", agent.session, agent.run.ConversationID, agent.run.Resumed, conversation)
	}
}

// ensureCurrent is what the updated app does at launch: its daemon ensure replaces the daemon of the older build.
func (u *releaseUpdate) ensureCurrent() *testworld.Peer {
	u.T.Helper()
	u.T.Cleanup(func() { u.Attn("daemon", "stop") })
	got := u.Attn("daemon", "ensure")
	if got.Code != 0 || !strings.Contains(got.Stdout, `"restarted"`) {
		u.T.Fatalf("attn daemon ensure from the update: %+v", got)
	}
	u.AwaitStopped()
	return u.App()
}

func turnOnTheSharedHost(t *testing.T, app *testworld.Peer) {
	t.Helper()
	mark := len(app.Log())
	setSetting(t, app, upgradeSharedHostEnabled, "true")
	testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings[upgradeSharedHostActive] == "true"
	})
}

func (u *releaseUpdate) spawnAgent(app *testworld.Peer, h fakeagent.Harness) releaseAgent {
	u.T.Helper()
	session := u.Spawn(app, h, u.Path(string(h)))
	return releaseAgent{session: session, run: u.Launched(session)}
}

func (u *releaseUpdate) assertSpawnRefused(app *testworld.Peer, remedy string) {
	u.T.Helper()
	result, _, _ := u.RequestSpawn(app, fakeagent.Claude, u.Path("refused"))
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "shared terminal host is unavailable") || !strings.Contains(protocol.Deref(result.Error), remedy) {
		u.T.Fatalf("a new terminal with no usable host answered %+v, want a refusal that says %q", result, remedy)
	}
	u.T.Logf("refused: %s", protocol.Deref(result.Error))
}

func (u *releaseUpdate) spawnShell(app *testworld.Peer) *releaseShell {
	u.T.Helper()
	session := u.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), u.Path("shell"))
	shell := &releaseShell{u: u, session: session}
	app.TypeLine(session, `for i in $(seq 1 300); do printf 'scroll-%03d\n' $i; done; echo printed-$((1+1))`)
	app.AwaitScreen(session, "printed-2")
	shell.pid = shell.currentPID(app)
	return shell
}

func (shell *releaseShell) currentPID(app *testworld.Peer) string {
	shell.u.T.Helper()
	shell.checks++
	path := filepath.Join(shell.u.Dir, "shell-"+shell.session+".pid")
	done := fmt.Sprintf("recorded-%d-$((1+1))", shell.checks)
	app.TypeLine(shell.session, "echo $$ > "+path+"; echo "+done)
	app.AwaitScreen(shell.session, fmt.Sprintf("recorded-%d-2", shell.checks))
	pid, err := os.ReadFile(path)
	if err != nil {
		shell.u.T.Fatal(err)
	}
	return strings.TrimSpace(string(pid))
}

func (shell *releaseShell) assertSameProcess(app *testworld.Peer) {
	shell.u.T.Helper()
	app.AwaitScreen(shell.session, "scroll-001")
	if got := shell.currentPID(app); got != shell.pid {
		shell.u.T.Errorf("the shell was pid %s and is now pid %s, want the same process", shell.pid, got)
	}
}

// turn proves input reaches the agent's process and that its hooks still drive its state.
func (u *releaseUpdate) turn(app *testworld.Peer, agent releaseAgent, phase string) {
	u.T.Helper()
	working := u.startTurn(app, agent, phase)
	agent.run.Reply(phase + " reply <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, working, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	app.AwaitScreen(agent.session, phase+" reply")
}

func (u *releaseUpdate) startTurn(app *testworld.Peer, agent releaseAgent, phase string) protocol.Session {
	u.T.Helper()
	mark := len(app.Log())
	app.TypeLine(agent.session, phase)
	if prompt := agent.run.Prompted(); prompt != phase {
		u.T.Fatalf("%s took %q, want %q", agent.run.Harness, prompt, phase)
	}
	return testworld.AwaitSessionSince(app, mark, agent.session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
}

func (u *releaseUpdate) parentPID(run *fakeagent.Run) string {
	u.T.Helper()
	ppid, err := exec.Command("ps", "-p", strconv.Itoa(run.PID), "-o", "ppid=").Output()
	if err != nil {
		u.T.Fatalf("read %s's parent: %v", run.Harness, err)
	}
	return strings.TrimSpace(string(ppid))
}

func (u *releaseUpdate) assertParent(run *fakeagent.Run, want string) {
	u.T.Helper()
	parent, err := exec.Command("ps", "-p", u.parentPID(run), "-o", "args=").Output()
	if err != nil {
		u.T.Fatalf("read %s's parent: %v", run.Harness, err)
	}
	if !strings.Contains(string(parent), want) {
		u.T.Errorf("%s's parent is %s, want %q", run.Harness, parent, want)
	}
}

func (u *releaseUpdate) assertSameParent(agent releaseAgent, want string) {
	u.T.Helper()
	if got := u.parentPID(agent.run); got != want {
		u.T.Errorf("%s's parent was pid %s and is now pid %s, want the same process", agent.run.Harness, want, got)
	}
}

func (u *releaseUpdate) assertStopsShowing(app *testworld.Peer, session, text string) {
	u.T.Helper()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })
	requireScreen(u.t, u.Attn("agent", "peek", session).Stdout, "screen at exit", text)
}

func (u *releaseUpdate) assertNoReloadNotice(app *testworld.Peer, session string) {
	u.T.Helper()
	if came := initialSession(u.t, app, session); protocol.Deref(came.TerminalBuildStale) {
		u.T.Errorf("session %s came back with the reload notice", session)
	}
}

func (u *releaseUpdate) assertNoSharedHost(terminal string) {
	u.T.Helper()
	paths, _ := filepath.Glob(filepath.Join(u.Dir, "pty-hosts", "*", "registry", terminal+".json"))
	if len(paths) != 0 {
		u.T.Fatalf("terminal %s is on the shared host: %v", terminal, paths)
	}
}

func (u *releaseUpdate) workerOf(terminal string) (registry string, pid int) {
	u.T.Helper()
	paths, _ := filepath.Glob(filepath.Join(u.Dir, "workers", "*", "registry", terminal+".json"))
	if len(paths) != 1 {
		u.T.Fatalf("terminal %s has %d worker registry entries, want one", terminal, len(paths))
	}
	entry, err := ptyworker.ReadRegistry(paths[0])
	if err != nil || entry.WorkerPID <= 0 {
		u.T.Fatalf("worker registry %s: %+v, %v", paths[0], entry, err)
	}
	return paths[0], entry.WorkerPID
}

const (
	upgradeSharedHostEnabled   = "pty_shared_host_enabled"
	upgradeSharedHostActive    = "pty_shared_host_active"
	upgradeDirectLaunchEnabled = "direct_agent_launch_enabled"
	upgradeMovedSnapshotFormat = "next"
)

type upgradeDaemon struct {
	t          *testing.T
	root       string
	port       int
	logOffset  int64
	instanceID string
	cmd        *exec.Cmd
	done       chan error
	ws         *websocket.Conn
	workers    map[int]bool
	agentPIDs  map[string]int
	terminals  map[string]string
	profileID  string
	stopped    bool
}

func startUpgradeDaemon(t *testing.T, root, binary, host string) *upgradeDaemon {
	t.Helper()
	var offset int64
	if info, err := os.Stat(filepath.Join(root, "daemon.log")); err == nil {
		offset = info.Size()
	}
	cmd := exec.Command(binary, "daemon")
	cmd.Dir = root
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if !strings.HasPrefix(key, "ATTN_") && key != "DEBUG" && key != "PATH" && key != "CODEX_HOME" {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "ATTN_DATA_DIR="+root, "ATTN_WS_PORT=0", "ATTN_HEADLESS_TASKS=0", "ATTN_WRAPPER_PATH="+binary, "SHELL=/bin/sh", "DEBUG=debug")
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, "ATTN_TOOL_HOME="+filepath.Join(root, "tools"))
	if host != "" {
		cmd.Env = append(cmd.Env, "ATTN_PTY_HOST_BINARY="+host)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &upgradeDaemon{t: t, root: root, logOffset: offset, cmd: cmd, done: make(chan error, 1)}
	go func() { d.done <- cmd.Wait() }()
	t.Cleanup(d.stop)
	d.waitForLog("WebSocket server starting", &stderr)
	// Historical daemons cannot inherit a listener or report the assigned port.
	// The readiness log follows bind, so inspect the captured PID once.
	inspect := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(cmd.Process.Pid), "-iTCP", "-sTCP:LISTEN", "-Fn")
	var inspectStderr bytes.Buffer
	inspect.Stderr = &inspectStderr
	output, err := inspect.Output()
	if err != nil {
		t.Fatalf("discover daemon %d listener: %v\n%s\n%s", cmd.Process.Pid, err, output, &inspectStderr)
	}
	var addresses []string
	for line := range strings.SplitSeq(string(output), "\n") {
		if address, ok := strings.CutPrefix(line, "n"); ok {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) != 1 {
		t.Fatalf("daemon %d has %d TCP listeners, want one: %s", cmd.Process.Pid, len(addresses), output)
	}
	_, port, err := net.SplitHostPort(addresses[0])
	if err != nil {
		t.Fatalf("daemon %d listener %q: %v", cmd.Process.Pid, addresses[0], err)
	}
	d.port, err = strconv.Atoi(port)
	if err != nil || d.port == 0 {
		t.Fatalf("daemon %d listener has invalid port %q: %v", cmd.Process.Pid, port, err)
	}
	d.connect()
	t.Logf("daemon binary=%s pid=%d ready instance=%s", binary, cmd.Process.Pid, d.instanceID)
	return d
}

func (d *upgradeDaemon) waitForLog(line string, stderr *bytes.Buffer) {
	d.t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		d.t.Fatal(err)
	}
	defer watcher.Close()
	if err := watcher.Add(d.root); err != nil {
		d.t.Fatal(err)
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		data := d.logSinceStart()
		if bytes.Contains(data, []byte(line)) {
			return
		}
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			if !os.IsNotExist(err) {
				d.t.Fatalf("daemon log watch: %v", err)
			}
		case err := <-d.done:
			d.stopped = true
			d.t.Fatalf("daemon exited while waiting for %q: %v\n%s\n%s", line, err, stderr, data)
		case <-deadline.C:
			d.t.Fatalf("daemon log never showed %q\n%s", line, data)
		}
	}
}

func (d *upgradeDaemon) logSinceStart() []byte {
	data, _ := os.ReadFile(filepath.Join(d.root, "daemon.log"))
	if int64(len(data)) < d.logOffset {
		return nil
	}
	return data[d.logOffset:]
}

func (d *upgradeDaemon) connect() {
	d.t.Helper()
	if d.ws != nil {
		_ = d.ws.CloseNow()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", d.port), nil)
	if err != nil {
		d.t.Fatal(err)
	}
	d.ws = ws
	d.ws.SetReadLimit(16 << 20)
	token, err := os.ReadFile(filepath.Join(d.root, "client-token"))
	if err != nil {
		d.t.Fatal(err)
	}
	d.write(map[string]any{"cmd": "client_hello", "client_kind": "pty-upgrade-test", "version": "test", "capabilities": []string{"workspace_sessions", "binary_pty_output"}, "client_token": strings.TrimSpace(string(token))})
	initial := d.event("initial_state", "")
	d.instanceID, _ = initial["daemon_instance_id"].(string)
	if d.instanceID == "" {
		d.t.Fatal("initial_state has no daemon instance identity")
	}
	d.profileID, _ = initial["selected_profile_id"].(string)
}

func (d *upgradeDaemon) notificationCount(kind string) int {
	d.t.Helper()
	result := d.command(map[string]any{"cmd": "notification_list", "request_id": "pty-host-notifications"}, "notification_list_result", "")
	notifications, _ := result["notifications"].([]any)
	count := 0
	for _, notification := range notifications {
		if entry, ok := notification.(map[string]any); ok && entry["kind"] == kind {
			count++
		}
	}
	return count
}

func (d *upgradeDaemon) stop() {
	if d.stopped {
		return
	}
	d.stopped = true
	if d.ws != nil {
		_ = d.ws.CloseNow()
	}
	_ = d.cmd.Process.Kill()
	select {
	case <-d.done:
	case <-time.After(fakeagent.HangGuard):
		d.t.Error("test daemon did not exit")
	}
}

func (d *upgradeDaemon) write(value any) {
	d.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		d.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.ws.Write(ctx, websocket.MessageText, data); err != nil {
		d.t.Fatal(err)
	}
}

func (d *upgradeDaemon) read(ctx context.Context) (map[string]any, string, []byte) {
	d.t.Helper()
	kind, data, err := d.ws.Read(ctx)
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(d.root, "daemon.log"))
		d.t.Fatalf("read daemon event: %v\n%s", err, log)
	}
	if kind == websocket.MessageBinary && len(data) > 0 && data[0] == protocol.BinaryFrameTypePtyOutput {
		id, _, output, err := protocol.DecodePtyOutputFrame(data)
		if err != nil {
			d.t.Fatal(err)
		}
		return nil, string(id), output
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		d.t.Fatalf("invalid event: %v: %q", err, data)
	}
	if event["event"] == "pty_output" {
		output, _ := base64.StdEncoding.DecodeString(fmt.Sprint(event["data"]))
		return event, fmt.Sprint(event["id"]), output
	}
	return event, "", nil
}

func (d *upgradeDaemon) event(name, id string) map[string]any {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		event, _, _ := d.read(ctx)
		if event["event"] == name && (id == "" || event["id"] == id) {
			if success, ok := event["success"].(bool); ok && !success {
				d.t.Fatalf("%s failed: %v\n%s", name, event, d.logSinceStart())
			}
			return event
		}
		if event["event"] == "error" {
			d.t.Fatalf("daemon command failed: %v", event)
		}
	}
}

func (d *upgradeDaemon) command(value any, event, id string) map[string]any {
	d.t.Helper()
	d.write(value)
	return d.event(event, id)
}

func (d *upgradeDaemon) spawn(id, agent, executable string, options ...func(map[string]any)) {
	d.t.Helper()
	spawn := map[string]any{"cmd": "spawn_session", "id": id, "cwd": d.root, "agent": agent, "executable": executable, "cols": 80, "rows": 24}
	for _, option := range options {
		option(spawn)
	}
	spawn["profile_id"] = d.profileID
	spawn["placement"] = map[string]any{}
	d.write(spawn)
	d.terminals[id] = d.placedTerminal(id)
	if entry, err := ptyworker.ReadRegistry(ptyhost.SessionRegistryPath(d.root, d.instanceID, d.terminals[id])); err == nil {
		d.workers[entry.WorkerPID] = true
	}
}

// placedTerminal waits for a profile spawn's result and the arrangement whose pane holds its terminal.
func (d *upgradeDaemon) placedTerminal(id string) string {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var terminal string
	spawned := false
	for !spawned || terminal == "" {
		event, _, _ := d.read(ctx)
		switch event["event"] {
		case "spawn_result":
			if event["id"] == id {
				if success, _ := event["success"].(bool); !success {
					d.t.Fatalf("spawn_result failed: %v", event)
				}
				spawned = true
			}
		case "profile_arrangement_changed":
			var arrangement struct {
				Desktops []protocol.Desktop `json:"desktops"`
			}
			data, _ := json.Marshal(event)
			_ = json.Unmarshal(data, &arrangement)
			for _, desktop := range arrangement.Desktops {
				for _, pane := range desktop.Panes {
					if string(pane.SessionID) == id && pane.RuntimeID != "" {
						terminal = string(pane.RuntimeID)
					}
				}
			}
		case "error":
			d.t.Fatalf("daemon command failed: %v", event)
		}
	}
	return terminal
}

func (d *upgradeDaemon) identity(id string) ptyworker.RegistryEntry {
	d.t.Helper()
	entry, err := ptyworker.ReadRegistry(ptyhost.SessionRegistryPath(d.root, d.instanceID, d.terminals[id]))
	if err != nil {
		d.t.Fatalf("%s registry: %v", id, err)
	}
	if entry.ChildPID <= 0 || entry.WorkerPID <= 0 {
		d.t.Fatalf("invalid process identity: %+v", entry)
	}
	d.workers[entry.WorkerPID] = true
	d.t.Logf("%s host=%d child=%d", id, entry.WorkerPID, entry.ChildPID)
	return entry
}

func (d *upgradeDaemon) assertIdentity(id string, want ptyworker.RegistryEntry) {
	d.t.Helper()
	got := d.identity(id)
	if got.ChildPID != want.ChildPID || got.WorkerPID != want.WorkerPID || got.DaemonInstanceID != want.DaemonInstanceID {
		d.t.Fatalf("surviving session %s moved: got=%+v want=%+v", id, got, want)
	}
}

func (d *upgradeDaemon) probe(id string, pid int, phase string) string {
	d.t.Helper()
	terminal := d.terminals[id]
	attached := d.command(map[string]any{"cmd": "attach_session", "id": terminal, "attach_policy": "same_app_remount"}, "attach_result", terminal)
	if attached["running"] != true || int(attached["pid"].(float64)) != pid {
		d.t.Fatalf("%s attached to the wrong process: %v", id, attached)
	}
	input := phase + "\n"
	want := fmt.Sprintf("__ACK_%d_%s__", pid, phase)
	if id != "shell" {
		want = "__ACK_" + terminal + "_" + phase + "_"
	}
	if id == "shell" {
		input = fmt.Sprintf("stty -echo; printf '__ACK_%%s_%%s__\\n' '%d' '%s'\n", pid, phase)
	}
	d.write(map[string]any{"cmd": "pty_input", "id": terminal, "data": input})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output strings.Builder
	ack := regexp.MustCompile(regexp.QuoteMeta(want) + `([0-9]+)__`)
	complete := func() bool {
		if id == "shell" {
			return strings.Contains(output.String(), want)
		}
		return ack.MatchString(output.String())
	}
	for !complete() {
		_, outputID, data := d.read(ctx)
		if outputID == terminal {
			output.Write(data)
		}
	}
	if id != "shell" {
		match := ack.FindStringSubmatch(output.String())
		if len(match) != 2 {
			d.t.Fatalf("missing agent PID acknowledgement: %q", output.String())
		}
		agentPID, _ := strconv.Atoi(match[1])
		if previous := d.agentPIDs[id]; previous != 0 && previous != agentPID {
			d.t.Fatalf("agent %s unexpectedly restarted: pid %d -> %d", id, previous, agentPID)
		}
		d.agentPIDs[id] = agentPID
	}
	d.t.Logf("%s acknowledged %s with original child %d", id, phase, pid)
	return output.String()
}
