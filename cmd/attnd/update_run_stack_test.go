package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/selfupdate"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/testworld"
)

const updateFixtureMakefile = `build-app:
	cp "$(TARGET_DIR)/attn" attn
	cp "$(TARGET_DIR)/attnd" attnd
	git rev-parse HEAD >> "$(BUILT)"
	@if [ -n "$(HOLD)" ]; then cat "$(HOLD)" >/dev/null; fi

install-staged:
	rm -rf "$(TREE)"
	mkdir -p "$(TREE)/bin"
	cp attn "$(TREE)/bin/attn"
	cp "$(or $(BROKEN),attnd)" "$(TREE)/bin/attnd"
	"$(TREE)/bin/attnd" daemon ensure
`

type sourceInstall struct {
	stack                         *testworld.Stack
	origin, checkout, tree, built string
	originHead                    string
	finished                      *testworld.Pause
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeFixtureFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func installFixtureTree(t *testing.T, tree, binary string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(tree, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"attn", "attnd"} {
		bytes, err := os.ReadFile(filepath.Join(filepath.Dir(binary), name))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(tree, "bin", name), string(bytes), 0o755)
	}
}

func newSourceInstall(t *testing.T, vars ...string) *sourceInstall {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the app tree sits under the real home outside Linux, where XDG_DATA_HOME does not move it")
	}
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Harness(protocol.SessionAgentShell)))
	dataHome := filepath.Join(stack.Dir, "share")
	install := &sourceInstall{
		stack:    stack,
		origin:   filepath.Join(stack.Dir, "origin.git"),
		checkout: filepath.Join(stack.Dir, "checkout"),
		tree:     filepath.Join(dataHome, "attn"),
		built:    filepath.Join(stack.Dir, "built-commits"),
	}
	runGit(t, stack.Dir, "init", "--bare", "-b", "main", install.origin)
	runGit(t, stack.Dir, "clone", install.origin, install.checkout)
	writeFixtureFile(t, filepath.Join(install.checkout, "Makefile"), updateFixtureMakefile, 0o644)
	writeFixtureFile(t, filepath.Join(install.checkout, ".gitignore"), "attn\nattnd\nbuild-cache\n", 0o644)
	runGit(t, install.checkout, "checkout", "-B", "main")
	runGit(t, install.checkout, "add", ".")
	runGit(t, install.checkout, "commit", "-m", "base")
	runGit(t, install.checkout, "push", "origin", "main")

	ahead := filepath.Join(stack.Dir, "ahead")
	runGit(t, stack.Dir, "clone", install.origin, ahead)
	writeFixtureFile(t, filepath.Join(ahead, "new-on-main"), "x", 0o644)
	runGit(t, ahead, "add", ".")
	runGit(t, ahead, "commit", "-m", "ahead")
	runGit(t, ahead, "push", "origin", "HEAD:main")
	install.originHead = runGit(t, ahead, "rev-parse", "HEAD")

	runGit(t, install.checkout, "checkout", "-b", "feature")
	writeFixtureFile(t, filepath.Join(install.checkout, "uncommitted"), "work in progress", 0o644)

	installFixtureTree(t, install.tree, testworld.AttnSourceBinaryFrom(t, "1.0.0", install.checkout))
	target := testworld.AttnSourceBinaryFrom(t, "2.0.0", install.checkout)
	stack.RunFrom(filepath.Join(install.tree, "bin", "attn"))
	stack.Vars = append(stack.Vars, append([]string{
		"XDG_DATA_HOME=" + dataHome,
		"TARGET_DIR=" + filepath.Dir(target),
		"BUILT=" + install.built,
		"TREE=" + install.tree,
	}, vars...)...)
	return install
}

func (i *sourceInstall) expectRestart() {
	i.finished = i.stack.PauseAt(pausepoint.UpdateApplyFinished)
}

func (i *sourceInstall) start(t *testing.T) *testworld.Peer {
	t.Helper()
	i.stack.Start()
	if result := i.stack.Attn("settings", "set", setting.SelfUpdateEnabled.Key, "true"); result.Code != 0 {
		t.Fatalf("turn on self-update: %+v", result)
	}
	app := i.stack.App()
	i.stack.FreeWSPort()
	return app
}

func startUpdate(t *testing.T, app *testworld.Peer) protocol.UpdateRun {
	t.Helper()
	result := sendStartUpdate(app)
	if !result.Success || result.Run == nil {
		t.Fatalf("start_update: %+v", result)
	}
	return *result.Run
}

func sendStartUpdate(app *testworld.Peer) protocol.StartUpdateResultMessage {
	mark := len(app.Log())
	app.Send(protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate, RequestID: protocol.Ptr("start")})
	return testworld.AwaitSince(app, mark, protocol.EventStartUpdateResult, func(protocol.StartUpdateResultMessage) bool { return true })
}

func awaitRunPhase(app *testworld.Peer, phase protocol.UpdatePhase) protocol.UpdateRun {
	return testworld.Await(app, protocol.EventUpdateChanged, func(m protocol.UpdateChangedMessage) bool { return m.Run.Phase == phase }).Run
}

func (i *sourceInstall) restarted() *testworld.Peer {
	resume := i.finished.Take()
	i.stack.AwaitDaemonReplaced()
	defer resume()
	return i.stack.App()
}

func finalRun(app *testworld.Peer) protocol.UpdateRun {
	finished := func(run protocol.UpdateRun) bool {
		return run.Phase == protocol.UpdatePhaseDone || run.Phase == protocol.UpdatePhaseFailed
	}
	if run := app.Initial.UpdateRun; run != nil && finished(*run) {
		return *run
	}
	return testworld.Await(app, protocol.EventUpdateChanged, func(m protocol.UpdateChangedMessage) bool { return finished(m.Run) }).Run
}

func updateRequestNotifications(app *testworld.Peer) []protocol.Notification {
	mark := len(app.Log())
	app.Send(protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr("update-list")})
	result := testworld.AwaitSince(app, mark, protocol.EventNotificationListResult, func(protocol.NotificationListResultMessage) bool { return true })
	var matching []protocol.Notification
	for _, n := range result.Notifications {
		if n.Kind == "update_requested" {
			matching = append(matching, n)
		}
	}
	return matching
}

func requestUpdateAs(t *testing.T, stack *testworld.Stack, session string) string {
	t.Helper()
	return requestUpdateBecause(t, stack, session, "needs the fix")
}

func requestUpdateBecause(t *testing.T, stack *testworld.Stack, session, reason string) string {
	t.Helper()
	result := stack.Run(testworld.Invocation{Args: []string{"update", "request", "--reason", reason}, Session: session})
	if result.Code != 0 {
		t.Fatalf("update request from %s: %+v", session, result)
	}
	return result.Stdout
}

func socketCommand(t *testing.T, stack *testworld.Stack, command any) protocol.Response {
	t.Helper()
	conn, err := stack.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(command); err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func inboxText(t *testing.T, stack *testworld.Stack, session string) string {
	t.Helper()
	result := stack.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: session})
	if result.Code != 0 {
		t.Fatalf("agent inbox for %s: %+v", session, result)
	}
	return result.Stdout
}

func TestAnUpdateRequestWaitsForTheUserAndDeclineClosesIt(t *testing.T) {
	install := newSourceInstall(t)
	app := install.start(t)
	shell := install.stack.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), install.stack.Path("shell"))

	requestUpdateAs(t, install.stack, shell)
	offered := updateRequestNotifications(app)
	if len(offered) != 1 || len(offered[0].Actions) != 2 || offered[0].Actions[0].Label != "Update" || offered[0].Actions[1].Label != "Decline" {
		t.Fatalf("offered notifications = %+v", offered)
	}
	if status := updateStatus(t, install.stack); status.Run != nil {
		t.Fatalf("a request started a run: %+v", status.Run)
	}
	t.Logf("offered: %+v", offered[0])

	for _, command := range []any{
		protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate},
		protocol.DeclineUpdateMessage{Cmd: protocol.CmdDeclineUpdate, UpdateRequestID: "any"},
	} {
		response := socketCommand(t, install.stack, command)
		if response.Ok || !strings.Contains(protocol.Deref(response.Error), "only the attn app") {
			t.Fatalf("the socket accepted %+v: %+v", command, response)
		}
	}
	if status := updateStatus(t, install.stack); status.Run != nil {
		t.Fatalf("the socket started a run: %+v", status.Run)
	}

	app.Send(protocol.DeclineUpdateMessage{Cmd: protocol.CmdDeclineUpdate, RequestID: protocol.Ptr("decline"), UpdateRequestID: offered[0].Actions[1].TargetID})
	testworld.Await(app, protocol.EventDeclineUpdateResult, func(m protocol.DeclineUpdateResultMessage) bool { return m.Success })
	if closed := updateRequestNotifications(app); closed[0].ReadAt == "" || len(closed[0].Actions) != 0 {
		t.Fatalf("Decline left the notification open: %+v", closed)
	}
	if status := updateStatus(t, install.stack); status.Run != nil {
		t.Fatalf("Decline started a run: %+v", status.Run)
	}
	if inbox := inboxText(t, install.stack, shell); !strings.Contains(inbox, "declined") {
		t.Fatalf("the requester was not told: %q", inbox)
	}
}

func TestEveryPendingRequestReasonReachesTheOneOpenNotification(t *testing.T) {
	install := newSourceInstall(t)
	app := install.start(t)
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	first := install.stack.Spawn(app, shell, install.stack.Path("first"))
	second := install.stack.Spawn(app, shell, install.stack.Path("second"))

	requestUpdateBecause(t, install.stack, first, "the parser fix is on main")
	if offered := updateRequestNotifications(app); len(offered) != 1 || !strings.Contains(offered[0].Trigger, "the parser fix is on main") {
		t.Fatalf("the first reason is not shown: %+v", offered)
	}

	requestUpdateBecause(t, install.stack, second, "the new queue key shipped")
	var open []protocol.Notification
	for _, n := range updateRequestNotifications(app) {
		if n.ReadAt == "" {
			open = append(open, n)
		}
	}
	if len(open) != 1 || len(open[0].Actions) != 2 {
		t.Fatalf("open notifications = %+v", open)
	}
	for _, reason := range []string{"the parser fix is on main", "the new queue key shipped"} {
		if !strings.Contains(open[0].Trigger, reason) {
			t.Fatalf("the notification does not show %q: %q", reason, open[0].Trigger)
		}
	}
}

func TestUpdateBuildsOriginMainLeavesTheCheckoutAloneAndJoinsRequesters(t *testing.T) {
	hold := filepath.Join(t.TempDir(), "hold")
	if err := exec.Command("mkfifo", hold).Run(); err != nil {
		t.Fatal(err)
	}
	install := newSourceInstall(t, "HOLD="+hold)
	install.expectRestart()
	app := install.start(t)
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	first := install.stack.Spawn(app, shell, install.stack.Path("first"))
	second := install.stack.Spawn(app, shell, install.stack.Path("second"))

	requestUpdateAs(t, install.stack, first)
	offered := updateRequestNotifications(app)
	if len(offered) != 1 {
		t.Fatalf("offered notifications = %+v", offered)
	}
	mark := len(app.Log())
	app.Send(protocol.StartUpdateMessage{Cmd: protocol.CmdStartUpdate, RequestID: protocol.Ptr("confirm"), UpdateRequestID: protocol.Ptr(offered[0].Actions[0].TargetID)})
	started := testworld.AwaitSince(app, mark, protocol.EventStartUpdateResult, func(protocol.StartUpdateResultMessage) bool { return true })
	if !started.Success || started.Run == nil || started.Run.Phase != protocol.UpdatePhasePreparing {
		t.Fatalf("Update on the notification: %+v", started)
	}
	if answered := updateRequestNotifications(app); answered[0].ReadAt == "" {
		t.Fatalf("Update left the notification open: %+v", answered)
	}

	joined := requestUpdateAs(t, install.stack, second)
	if !strings.Contains(joined, "already running") {
		t.Fatalf("a second request did not join the run: %q", joined)
	}
	again := sendStartUpdate(app)
	if !again.Success || again.Run.ID != started.Run.ID {
		t.Fatalf("a second start_update did not join the open run: %+v", again)
	}

	branchBefore := runGit(t, install.checkout, "rev-parse", "--abbrev-ref", "HEAD")
	headBefore := runGit(t, install.checkout, "rev-parse", "HEAD")
	writeFixtureFile(t, hold, "", 0o644)
	run := awaitRunPhase(app, protocol.UpdatePhaseApplying)
	if run.TargetBuild == "" {
		t.Fatalf("applying with no target: %+v", run)
	}

	app = install.restarted()
	done := finalRun(app)
	t.Logf("final run: %+v", done)
	if done.Phase != protocol.UpdatePhaseDone || done.TargetBuild == "" || !strings.HasPrefix(done.TargetBuild, "2.0.0 ") {
		t.Fatalf("final run = %+v", done)
	}
	status := updateStatus(t, install.stack)
	if status.Version != "2.0.0" || status.Run == nil || status.Run.Phase != protocol.UpdatePhaseDone {
		t.Fatalf("status after the update = %+v", status)
	}
	builtBytes, err := os.ReadFile(install.built)
	if err != nil {
		t.Fatal(err)
	}
	if built := strings.TrimSpace(string(builtBytes)); built != install.originHead {
		t.Fatalf("built %q, want origin/main %q", built, install.originHead)
	}
	if got := runGit(t, install.checkout, "rev-parse", "--abbrev-ref", "HEAD"); got != branchBefore || got != "feature" {
		t.Fatalf("checkout branch = %q", got)
	}
	if got := runGit(t, install.checkout, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("checkout HEAD moved to %q", got)
	}
	if got := runGit(t, install.checkout, "status", "--porcelain"); !strings.Contains(got, "uncommitted") {
		t.Fatalf("the uncommitted file went away: %q", got)
	}
	for _, session := range []string{first, second} {
		if inbox := inboxText(t, install.stack, session); !strings.Contains(inbox, "attn update finished") {
			t.Fatalf("session %s got no final notice: %q", session, inbox)
		}
	}
}

func TestARepeatUpdateReusesTheWorktree(t *testing.T) {
	install := newSourceInstall(t)
	install.expectRestart()
	app := install.start(t)
	startUpdate(t, app)
	app = install.restarted()
	if run := finalRun(app); run.Phase != protocol.UpdatePhaseDone {
		t.Fatalf("first update: %+v", run)
	}
	worktree := filepath.Join(install.stack.Dir, "update-source")
	cache := filepath.Join(worktree, "build-cache")
	writeFixtureFile(t, cache, "kept", 0o644)

	repeat := sendStartUpdate(app)
	if !repeat.Success {
		t.Fatalf("second start_update: %+v", repeat)
	}
	run := testworld.Await(app, protocol.EventUpdateChanged, func(m protocol.UpdateChangedMessage) bool {
		return m.Run.ID == repeat.Run.ID && m.Run.Phase == protocol.UpdatePhaseDone
	}).Run
	t.Logf("repeat run: %+v", run)
	if !strings.Contains(protocol.Deref(run.Outcome), "Already running") {
		t.Fatalf("repeat outcome = %q", protocol.Deref(run.Outcome))
	}
	if got := runGit(t, worktree, "rev-parse", "HEAD"); got != install.originHead {
		t.Fatalf("worktree HEAD %q, want %q", got, install.originHead)
	}
	if lines := strings.Fields(mustRead(t, install.built)); len(lines) != 2 {
		t.Fatalf("builds recorded: %v", lines)
	}
	if mustRead(t, cache) != "kept" {
		t.Fatal("the repeat run lost the build cache")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpdateRefusesARepositoryItCannotUseAndChangesNothing(t *testing.T) {
	for _, broken := range []struct {
		name   string
		damage func(t *testing.T, install *sourceInstall)
		want   string
	}{
		{"no origin", func(t *testing.T, install *sourceInstall) { runGit(t, install.checkout, "remote", "remove", "origin") }, "no origin remote"},
		{"removed repository", func(t *testing.T, install *sourceInstall) {
			if err := os.RemoveAll(install.checkout); err != nil {
				t.Fatal(err)
			}
		}, "is gone"},
	} {
		t.Run(broken.name, func(t *testing.T) {
			install := newSourceInstall(t)
			app := install.start(t)
			before := mustRead(t, filepath.Join(install.tree, "bin", "attnd"))
			broken.damage(t, install)
			refused := sendStartUpdate(app)
			t.Logf("refusal: %+v", refused)
			if refused.Success || !strings.Contains(protocol.Deref(refused.Error), broken.want) || !strings.Contains(protocol.Deref(refused.Error), install.checkout) {
				t.Fatalf("start_update = %+v, want a refusal naming %s and %q", refused, install.checkout, broken.want)
			}
			if status := updateStatus(t, install.stack); status.Run != nil || status.Version != "1.0.0" {
				t.Fatalf("a refused update changed state: %+v", status)
			}
			if _, err := os.Stat(filepath.Join(install.stack.Dir, "update-source")); !os.IsNotExist(err) {
				t.Fatalf("a refused update made a worktree: %v", err)
			}
			if mustRead(t, filepath.Join(install.tree, "bin", "attnd")) != before {
				t.Fatal("a refused update changed the install")
			}
		})
	}
}

func TestADaemonOutsideItsAppTreeIsRefusedAndNothingAroundItChanges(t *testing.T) {
	install := newSourceInstall(t)
	outside := filepath.Join(install.stack.Dir, "dot-local")
	installFixtureTree(t, outside, testworld.AttnSourceBinaryFrom(t, "1.0.0", install.checkout))
	neighbor := filepath.Join(outside, "bin", "someone-elses-tool")
	writeFixtureFile(t, neighbor, "keep me", 0o755)
	install.stack.RunFrom(filepath.Join(outside, "bin", "attn"))
	app := install.start(t)

	refused := sendStartUpdate(app)
	t.Logf("refusal: %+v", refused)
	for _, named := range []string{filepath.Join(outside, "bin"), install.tree, "make install"} {
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), named) {
			t.Fatalf("start_update = %+v, want a refusal naming %q", refused, named)
		}
	}
	if mustRead(t, neighbor) != "keep me" {
		t.Fatal("a refused update touched the directory around the daemon")
	}
	if status := updateStatus(t, install.stack); status.Run != nil {
		t.Fatalf("a refused update opened a run: %+v", status.Run)
	}
}

func TestABuildPastItsLimitFailsAndNamesTheLimit(t *testing.T) {
	hold := filepath.Join(t.TempDir(), "hold")
	if err := exec.Command("mkfifo", hold).Run(); err != nil {
		t.Fatal(err)
	}
	install := newSourceInstall(t, "HOLD="+hold, selfupdate.BuildTimeoutEnv+"=200ms")
	app := install.start(t)
	before := mustRead(t, filepath.Join(install.tree, "bin", "attnd"))

	run := startUpdate(t, app)
	failed := awaitRunPhase(app, protocol.UpdatePhaseFailed)
	t.Logf("failed run: %+v", failed)
	for _, named := range []string{"200ms", selfupdate.BuildTimeoutEnv, run.LogPath} {
		if !strings.Contains(protocol.Deref(failed.Outcome), named) {
			t.Fatalf("outcome = %q, want %q", protocol.Deref(failed.Outcome), named)
		}
	}
	if mustRead(t, filepath.Join(install.tree, "bin", "attnd")) != before {
		t.Fatal("a build past its limit changed the install")
	}
}

func TestATargetThatFailsToStartRollsBackAndLeavesTerminalsRunning(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken-attnd")
	install := newSourceInstall(t, "BROKEN="+broken)
	writeFixtureFile(t, broken, "#!/bin/sh\n\"$TARGET_DIR/attnd\" daemon stop\necho the new build cannot start >&2\nexit 1\n", 0o755)
	install.expectRestart()
	app := install.start(t)
	shell := install.stack.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), install.stack.Path("shell"))
	app.TypeLine(shell, "kept=yes; printf '__before_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__before_yes")

	startUpdate(t, app)
	app = install.restarted()
	failed := finalRun(app)
	t.Logf("failed run: %+v", failed)
	if failed.Phase != protocol.UpdatePhaseFailed || !strings.Contains(protocol.Deref(failed.Outcome), "went back") {
		t.Fatalf("final run = %+v, want failed naming the rollback", failed)
	}
	if status := updateStatus(t, install.stack); status.Version != "1.0.0" {
		t.Fatalf("running version after rollback = %s", status.Version)
	}
	app.Watch(shell)
	app.TypeLine(shell, "printf '__after_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__after_yes")
}

func TestATargetTheDatabaseRefusesStopsBeforeAnySwap(t *testing.T) {
	install := newSourceInstall(t)
	app := install.start(t)
	before := mustRead(t, filepath.Join(install.tree, "bin", "attnd"))
	db, err := sql.Open("sqlite3", filepath.Join(install.stack.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const future = 9999999999999999
	if _, err := db.Exec(fmt.Sprintf(`INSERT INTO schema_migrations (version, applied_at) VALUES (%d, '2030-01-01')`, future)); err != nil {
		t.Fatal(err)
	}

	startUpdate(t, app)
	failed := awaitRunPhase(app, protocol.UpdatePhaseFailed)
	t.Logf("failed run: %+v", failed)
	if !strings.Contains(protocol.Deref(failed.Outcome), fmt.Sprint(future)) || !strings.Contains(protocol.Deref(failed.Outcome), "2.0.0") {
		t.Fatalf("outcome = %q, want the migration and the build", protocol.Deref(failed.Outcome))
	}
	if mustRead(t, filepath.Join(install.tree, "bin", "attnd")) != before {
		t.Fatal("the install changed before the database check")
	}
}

func TestTheUpdateHelperIsRefusedInsideASession(t *testing.T) {
	install := newSourceInstall(t)
	install.start(t)
	pidPath := filepath.Join(install.stack.Dir, "attn.pid")
	before := mustRead(t, pidPath)
	target := testworld.AttnSourceBinaryFrom(t, "2.0.0", install.checkout)
	result := install.stack.Run(testworld.Invocation{
		Binary: filepath.Join(filepath.Dir(target), "attnd"),
		Args:   []string{"_update-apply", "--run", "r", "--dir", install.checkout, "--root", install.tree},
		Env:    []string{"ATTN_SESSION_HOME=" + install.stack.Dir, "ATTN_SESSION_BINARY=" + filepath.Join(install.tree, "bin", "attnd")},
	})
	t.Logf("refusal: %+v", result)
	if result.Code == 0 || !strings.Contains(result.Stderr, "refusing to start a daemon") {
		t.Fatalf("the helper ran inside a session: %+v", result)
	}
	if after := mustRead(t, pidPath); after != before {
		t.Fatalf("daemon pid changed from %s to %s", before, after)
	}
	if status := updateStatus(t, install.stack); status.Version != "1.0.0" {
		t.Fatalf("version after the refusal = %s", status.Version)
	}
}
