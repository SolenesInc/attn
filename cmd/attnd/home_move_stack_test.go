package main_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type movingPair struct {
	*testworld.OutpostPair
	t       *testing.T
	shellH  fakeagent.Harness
	onHome  string
	onOut   string
	pidHome string
	pidOut  string
}

func machineTools(t *testing.T, s *testworld.Stack, tools map[string]string) {
	t.Helper()
	bin := filepath.Join(s.Dir, "machine-bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := os.Getenv("PATH")
	for _, v := range s.Vars {
		if strings.HasPrefix(v, "PATH=") {
			path = strings.TrimPrefix(v, "PATH=")
		}
	}
	s.Vars = append(s.Vars, "PATH="+bin+":"+path)
}

// systemctlFake logs each call to log, and answers is-enabled from the last enable or disable it logged for that
// unit, as systemd keeps that state across daemon restarts.
func systemctlFake(log string) string {
	return `echo "$@" >> '` + log + `'
if [ "$1 $2 $3" = "--user is-enabled --quiet" ]; then
	last=$(grep -E -- "^--user (enable|disable) $4$" '` + log + `' | tail -n 1)
	[ "$last" = "--user enable $4" ]
fi`
}

// forgetEnabled has systemd on s forget that attn.service is enabled, as an install stopped between writing the unit
// and enabling it leaves it.
func forgetEnabled(t *testing.T, s *testworld.Stack) {
	t.Helper()
	log, err := os.OpenFile(filepath.Join(s.Dir, "systemctl.log"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := log.WriteString("--user disable attn.service\n"); err != nil {
		t.Fatal(err)
	}
}

// requireUnitEnabled fails unless the last systemctl call on s that enabled or disabled attn.service enabled it.
func requireUnitEnabled(t *testing.T, s *testworld.Stack) {
	t.Helper()
	if last, calls := lastEnablement(s); last != "enable" {
		t.Fatalf("the new home's unit is not enabled; systemctl calls:\n%s", calls)
	}
}

// lastEnablement is enable or disable, whichever the last systemctl call on s for attn.service was, with every call.
func lastEnablement(s *testworld.Stack) (string, string) {
	calls, _ := os.ReadFile(filepath.Join(s.Dir, "systemctl.log"))
	last := ""
	for _, line := range strings.Split(string(calls), "\n") {
		if verb, ok := strings.CutSuffix(strings.TrimPrefix(line, "--user "), " attn.service"); ok && (verb == "enable" || verb == "disable") {
			last = verb
		}
	}
	return last, string(calls)
}

func healthyMachine(t *testing.T, s *testworld.Stack) {
	s.Vars = append(s.Vars, "XDG_CONFIG_HOME="+s.Path("xdg-config"))
	machineTools(t, s, map[string]string{
		"gh":        "exit 0",
		"loginctl":  "echo yes",
		"systemctl": systemctlFake(filepath.Join(s.Dir, "systemctl.log")),
	})
}

func newMovingPair(t *testing.T) *movingPair {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{home, outpost} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	m := &movingPair{OutpostPair: pair, t: t, shellH: fakeagent.Harness(protocol.SessionAgentShell)}
	m.onHome = home.Spawn(pair.App, m.shellH, home.Path("shell"))
	m.onOut = home.Spawn(pair.App, m.shellH, outpost.Path("shell"), func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	m.pidHome, m.pidOut = home.Path("h.pid"), outpost.Path("o.pid")
	m.echo(pair.App, "before")
	return m
}

func (m *movingPair) echo(app *testworld.Peer, tag string) (string, string) {
	m.t.Helper()
	app.TypeLine(m.onHome, "echo $$ > "+m.pidHome+"; echo "+tag+"-home-$((1+1))")
	app.TypeLine(m.onOut, "echo $$ > "+m.pidOut+"; echo "+tag+"-out-$((2+2))")
	app.AwaitScreen(m.onHome, tag+"-home-2")
	app.AwaitScreen(m.onOut, tag+"-out-4")
	return pidOf(m.t, m.pidHome), pidOf(m.t, m.pidOut)
}

func homeMoveJSON(t *testing.T, s *testworld.Stack, args ...string) protocol.HomeMove {
	t.Helper()
	result := s.Attn(append([]string{"home"}, append(args, "--json")...)...)
	var move protocol.HomeMove
	if err := json.Unmarshal([]byte(result.Stdout), &move); err != nil {
		t.Fatalf("attn home %v: %+v (%v)", args, result, err)
	}
	return move
}

func awaitConnected(t *testing.T, app *testworld.Peer, machine string) {
	t.Helper()
	awaitOutpost(t, app, machine, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
}

func awaitOutpost(t *testing.T, app *testworld.Peer, machine string, want func(protocol.OutpostInfo) bool) {
	t.Helper()
	shows := func(rows []protocol.OutpostInfo) bool {
		return slices.ContainsFunc(rows, func(r protocol.OutpostInfo) bool { return r.ID == machine && want(r) })
	}
	if !shows(app.Initial.Outposts) {
		testworld.Await(app, protocol.EventOutpostsUpdated, func(msg protocol.OutpostsUpdatedMessage) bool { return shows(msg.Outposts) })
	}
}

func role(t *testing.T, s *testworld.Stack) enrollment.Status {
	t.Helper()
	status, err := enrollment.Load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestHomeMoveKeepsTerminalsAndMovesBack(t *testing.T) {
	m := newMovingPair(t)
	home, outpost := m.Home, m.Outpost
	homeMachine := role(t, home).MachineID
	beforeH, beforeO := m.echo(m.App, "start")

	awaitMachineNotebook(t, m.App, m.Host, protocol.OutpostNotebookStateInSync)
	move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome")
	if move.Step != "committed" || move.Error != nil {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()
	checking := outpost.PauseAt(pausepoint.HomeMoveChecking)
	home.Start()
	outpost.Start()
	if !role(t, outpost).IsHome() || role(t, home).IsHome() {
		t.Fatalf("roles after the move: home %+v, outpost %+v", role(t, home), role(t, outpost))
	}
	forgetEnabled(t, outpost)
	outpost.Stop()
	outpost.Start()
	requireUnitEnabled(t, outpost)
	app := outpost.App()
	awaitConnected(t, app, homeMachine)
	if a, b := m.echo(app, "moved"); a != beforeH || b != beforeO {
		t.Fatalf("pids after the move: %s->%s, %s->%s", beforeH, a, beforeO, b)
	}
	if done := homeMoveJSON(t, home, "status"); done.Step != "done" {
		t.Fatalf("the old home's move once it bridged to the new home: %s", describeMove(done))
	}
	if refused := home.Attn("home", "rollback"); refused.Code == 0 || !strings.Contains(refused.Stderr, "attn home move") {
		t.Fatalf("rollback of a done move: exit %d\n%s%s", refused.Code, refused.Stdout, refused.Stderr)
	}
	if refused := outpost.Attn("home", "rollback"); refused.Code == 0 || !strings.Contains(refused.Stderr, "attn home move") {
		t.Fatalf("rollback on the new home: exit %d\n%s%s", refused.Code, refused.Stdout, refused.Stderr)
	}
	unit := filepath.Join(outpost.Path("xdg-config"), "systemd", "user", "attn.service")
	if _, err := os.Stat(unit); err != nil {
		t.Fatalf("the new home's unit: %v", err)
	}

	awaitMachineNotebook(t, app, homeMachine, protocol.OutpostNotebookStateInSync)
	app.Send(protocol.HomeMoveMessage{Cmd: protocol.CmdHomeMove, Machine: homeMachine, Via: protocol.Ptr("outpost"), RequestID: protocol.Ptr("back")})
	checking.Await()
	started := homeMoveJSON(t, outpost, "status")
	checking.Release()
	if started.Step != "checking" || protocol.Deref(started.Side) != "from" || started.ID == nil {
		t.Fatalf("status while the move back runs its checks: %s", describeMove(started))
	}
	result := testworld.Await(app, protocol.EventHomeMoveResult, func(r protocol.HomeMoveResultMessage) bool { return r.RequestID == "back" })
	if !result.Success || result.HomeMove == nil || result.HomeMove.Step != "committed" || result.HomeMove.Error != nil {
		t.Fatalf("move back from the app: %+v", result)
	}
	for _, step := range []string{"fence", "copy", "build", "commit"} {
		progress := testworld.Await(app, protocol.EventHomeMoveProgress, func(p protocol.HomeMoveProgressMessage) bool { return p.RequestID == "back" && p.Step == step })
		if progress.ID != protocol.Deref(result.HomeMove.ID) || progress.ID != protocol.Deref(started.ID) {
			t.Fatalf("step %s names move %q; the move is %q", step, progress.ID, protocol.Deref(result.HomeMove.ID))
		}
	}
	outpost.AwaitStopped()
	home.AwaitStopped()
	finished := outpost.PassesOf(pausepoint.HomeMoveFinished)
	outpost.Start()
	home.Start()
	if !role(t, home).IsHome() || role(t, outpost).IsHome() {
		t.Fatalf("roles after moving back: home %+v, outpost %+v", role(t, home), role(t, outpost))
	}
	app = home.App()
	awaitConnected(t, app, m.Host)
	if a, b := m.echo(app, "back"); a != beforeH || b != beforeO {
		t.Fatalf("pids after moving back: %s->%s, %s->%s", beforeH, a, beforeO, b)
	}
	<-finished
	if _, err := os.Stat(unit); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the unit that started the home is still installed once the home moved back: %v", err)
	}
	if last, calls := lastEnablement(outpost); last != "disable" {
		t.Fatalf("systemctl calls on the machine the home left:\n%s", calls)
	}
}

func TestHomeMoveBackKeepsTheClosedSessionsOfTheMachineItReturnsTo(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	homeMachine := role(t, home).MachineID
	id := string(home.Spawn(pair.App, fakeagent.Claude, home.Path("work")))
	run := home.Launched(id)
	pair.App.TypeLine(id, "Remember this conversation")
	run.Prompted()
	run.Reply("done <!-- attn:state=waiting_input -->")
	if closed := home.Attn("agent", "close", id, "-m", "finished", "--source-session", id); closed.Code != 0 {
		t.Fatalf("close: %+v", closed)
	}
	before := closedUsage(t, home, id)
	if before == "" {
		t.Fatal("the closed session has no usage before any move")
	}

	awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)
	if move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()
	home.Start()
	outpost.Start()
	app := outpost.App()
	awaitConnected(t, app, homeMachine)
	awaitMachineNotebook(t, app, homeMachine, protocol.OutpostNotebookStateInSync)
	if back := homeMoveJSON(t, outpost, "move", homeMachine, "--via", "outpost"); back.Step != "committed" {
		t.Fatalf("move back: %s", describeMove(back))
	}
	outpost.AwaitStopped()
	home.AwaitStopped()
	outpost.Start()
	home.Start()
	if after := closedUsage(t, home, id); after != before {
		t.Fatalf("the closed session's usage on the machine the home came back to: %s, was %s", after, before)
	}
}

// closedUsage is the usage the session ledger shows for a closed session, which its runtime row holds.
func closedUsage(t *testing.T, s *testworld.Stack, id string) string {
	t.Helper()
	var ledger struct {
		Entries []struct {
			ID    string          `json:"id"`
			Usage json.RawMessage `json:"usage"`
		} `json:"entries"`
	}
	s.Attn("session", "list", "--closed", "--json").JSON(t, &ledger)
	for _, entry := range ledger.Entries {
		if entry.ID == id {
			return string(entry.Usage)
		}
	}
	t.Fatalf("the session ledger has no closed session %s", id)
	return ""
}

func TestHomeMoveRollbackKeepsOneHomeAtATime(t *testing.T) {
	cloud := testworld.StartCloud(t)
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{home, outpost} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash", "ATTN_CLOUD_URL="+cloud.HomeURL)
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	setPhoneAccess(t, pair.App, true)
	testworld.Await(pair.App, protocol.EventPhonesUpdated, linkConnected)
	awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)
	if move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()
	pair.SSH.Fail("oldhome", "ssh: connect to host oldhome port 22: No route to host")
	pair.SSH.Fail(pair.Destination, "ssh: connect to host outpost port 22: No route to host")
	home.Start()
	outpost.Start()
	awaitLinked(outpost.App())
	if back := homeMoveJSON(t, home, "rollback"); back.Step != "rolling-back" {
		t.Fatalf("rollback: %s", describeMove(back))
	}
	home.AwaitStopped()
	home.Start()
	pair.SSH.Restore(pair.Destination)
	redial(home)
	outpost.AwaitStopped()
	outpost.Start()
	defer home.FireTimers(bridge.RetryTimer)()
	awaitLinked(home.App())
	if !role(t, home).IsHome() || role(t, outpost).IsHome() {
		t.Fatalf("roles: old home %+v, target %+v; want the old home home", role(t, home), role(t, outpost))
	}
	if most := cloud.MostOpen(); most != 1 {
		t.Fatalf("the Cloud Relay saw %d home sockets at once while the home rolled back:\n%s", most, cloud.Connections())
	}
}

func TestARolledBackHomeShowsItsWaitAndASecondRollbackEndsIt(t *testing.T) {
	cloud := testworld.StartCloud(t)
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{home, outpost} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash", "ATTN_CLOUD_URL="+cloud.HomeURL)
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	setPhoneAccess(t, pair.App, true)
	testworld.Await(pair.App, protocol.EventPhonesUpdated, linkConnected)
	awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)
	if move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()
	pair.SSH.Fail(pair.Destination, "ssh: connect to host outpost port 22: No route to host")
	home.Start()
	if back := homeMoveJSON(t, home, "rollback"); back.Step != "rolling-back" {
		t.Fatalf("rollback: %s", describeMove(back))
	}
	home.AwaitStopped()
	home.Start()
	app := home.App()
	if waiting := homeMoveJSON(t, home, "status"); waiting.Step != "rolled-back" || !protocol.Deref(waiting.WaitsForTarget) {
		t.Fatalf("a home that rolled back while its target is away: %s", describeMove(waiting))
	}
	requireStdout(t, home.Attn("home", "status"), "run `attn home rollback` again to stop waiting")
	shown := testworld.Request[protocol.PhonesUpdatedMessage](app, protocol.PhonesGetMessage{Cmd: protocol.CmdPhonesGet}, protocol.EventPhonesUpdated, nil)
	if linkConnected(shown) || !strings.Contains(protocol.Deref(shown.Link.Error), "waits until") {
		t.Fatalf("the phone link of a home waiting for its target: %+v", shown.Link)
	}
	if released := homeMoveJSON(t, home, "rollback"); released.Step != "rolled-back" || released.WaitsForTarget != nil {
		t.Fatalf("a second rollback: %s", describeMove(released))
	}
	awaitLinked(app)
}

// awaitLinked waits until the daemon behind app holds the home's Cloud Relay link.
func awaitLinked(app *testworld.Peer) {
	if shown := testworld.Request[protocol.PhonesUpdatedMessage](app, protocol.PhonesGetMessage{Cmd: protocol.CmdPhonesGet}, protocol.EventPhonesUpdated, nil); linkConnected(shown) {
		return
	}
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
}

func describeMove(move protocol.HomeMove) string {
	data, _ := json.Marshal(move)
	return string(data)
}

func notebookDirs(t *testing.T, s *testworld.Stack) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(s.Dir, "notebook*"))
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

func identityFiles(t *testing.T, s *testworld.Stack) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{"daemon-id", "agent-key", "bridge_host_key"} {
		data, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatalf("%s on %s: %v", name, s.Dir, err)
		}
		files[name] = string(data)
	}
	return files
}

func worktreeListed(t *testing.T, app *testworld.Peer, path, id string) bool {
	t.Helper()
	listed := testworld.Request(app, protocol.WorktreeListMessage{Cmd: protocol.CmdWorktreeList, RequestID: protocol.Ptr(id)},
		protocol.EventWorktreeListResult, func(r protocol.WorktreeListResultEvent) bool { return protocol.Deref(r.RequestID) == id })
	for _, wt := range listed.WorktreeListResult.Worktrees {
		if wt.Path == path {
			return true
		}
	}
	return false
}

// manyPluginFiles names more files than fit in one ssh packet of 256 KiB, at about 130 bytes of manifest each.
const manyPluginFiles = 3000

func TestHomeMoveTakesTheWholeHomeAlong(t *testing.T) {
	cloud := testworld.StartCloud(t)
	home, outpost, third := testworld.NewStack(t), testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{home, outpost, third} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash", "ATTN_CLOUD_URL="+cloud.HomeURL)
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	third.Start()
	pair.Start()
	app := pair.App
	thirdID := pair.Enroll("third", "third", third)
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")
	homeMachine := role(t, home).MachineID

	repo := outpost.Path("shop")
	gitRepo(t, repo)
	worktree := outpost.Path("shop--feat")
	created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, Host: protocol.Ptr(pair.Host), MainRepo: repo, Branch: "feat", Path: protocol.Ptr(worktree), RequestID: protocol.Ptr("create")},
		protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == "create" })
	if !created.Success {
		t.Fatalf("create worktree on the outpost: %s", protocol.Deref(created.Error))
	}
	requireStdout(t, outpost.Attn("settings", "set", "worktree_sweep_enabled", "false"), "false")
	targetApp := outpost.App()
	for len(analyticsRows(t, targetApp)) == 0 {
		requireStdout(t, outpost.Attn("settings", "get", "theme"))
	}
	analyticsBefore := analyticsRows(t, targetApp)

	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	profile := app.SelectedProfile()
	qr := testworld.ParsePhoneQR(t, beginPairing(t, app, profile, "pair").Qr)
	phone := testworld.NewPhone(t, cloud, "Test iPhone")
	socket, err := phone.DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()

	awaitMachineNotebook(t, app, pair.Host, protocol.OutpostNotebookStateInSync)
	awaitMachineNotebook(t, app, thirdID, protocol.OutpostNotebookStateInSync)
	notebooks := map[*testworld.Stack][]string{home: notebookDirs(t, home), outpost: notebookDirs(t, outpost), third: notebookDirs(t, third)}
	identities := map[*testworld.Stack]map[string]string{home: identityFiles(t, home), outpost: identityFiles(t, outpost), third: identityFiles(t, third)}

	tool := filepath.Join(home.Dir, "plugins", "tool")
	if err := os.MkdirAll(tool, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tool, "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tool", filepath.Join(home.Dir, "plugins", "linked")); err != nil {
		t.Fatal(err)
	}
	askImage := filepath.Join("asks", "a-moved", "image.png")
	if err := os.MkdirAll(filepath.Join(home.Dir, filepath.Dir(askImage)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, askImage), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	analyticsSecret, err := os.ReadFile(filepath.Join(home.Dir, "analytics-secret"))
	if err != nil {
		t.Fatal(err)
	}
	homeAnalytics := analyticsRows(t, app)
	many := filepath.Join(home.Dir, "plugins", "many")
	if err := os.MkdirAll(many, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range manyPluginFiles {
		if err := os.WriteFile(filepath.Join(many, fmt.Sprintf("module-%04d.js", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(home.Dir, "quick-captures")); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(outpost.Dir, "quick-captures", "from-its-own-home.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome")
	if move.Step != "committed" || move.Error != nil {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()
	home.Start()
	outpost.Start()

	newHome := outpost.App()
	awaitConnected(t, newHome, homeMachine)
	awaitConnected(t, newHome, thirdID)
	t.Run("the third outpost bridges to the new home without leave", func(t *testing.T) {
		status := role(t, third)
		if !status.IsOutpost() || status.HomeID != role(t, outpost).HomeID || status.HomeDaemonID != strings.TrimSpace(identities[outpost]["daemon-id"]) {
			t.Fatalf("third outpost's enrollment after the move: %+v", status)
		}
	})
	t.Run("a paired phone reads from the new home", func(t *testing.T) {
		socket.AwaitPresence(true)
		if err := socket.Resume(qr); err != nil {
			t.Fatal(err)
		}
		if welcome := socket.Hello(); welcome.Profile.ID != profile {
			t.Fatalf("welcome from the new home: %+v", welcome)
		}
		if most := cloud.MostOpen(); most != 1 {
			t.Fatalf("the Cloud Relay saw %d home sockets at once:\n%s", most, cloud.Connections())
		}
	})
	t.Run("a home with more files than one ssh packet can name moves them all", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(outpost.Dir, "plugins", "many", fmt.Sprintf("module-%04d.js", manyPluginFiles-1))); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a root the home lacks leaves the target's own copy behind", func(t *testing.T) {
		if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the target's own quick capture is in the new home: %v", err)
		}
	})
	t.Run("files keep their mode and links, with the analytics keys and ask images", func(t *testing.T) {
		if info, err := os.Stat(filepath.Join(outpost.Dir, "plugins", "tool", "run")); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("an executable plugin file on the new home: %v %v", info, err)
		}
		if link, err := os.Readlink(filepath.Join(outpost.Dir, "plugins", "linked")); err != nil || link != "tool" {
			t.Fatalf("a linked plugin on the new home: %q %v", link, err)
		}
		if data, err := os.ReadFile(filepath.Join(outpost.Dir, askImage)); err != nil || string(data) != "png" {
			t.Fatalf("an ask's image on the new home: %q %v", data, err)
		}
		if moved, err := os.ReadFile(filepath.Join(outpost.Dir, "analytics-secret")); err != nil || string(moved) != string(analyticsSecret) {
			t.Fatalf("the analytics secret on the new home changed: %v", err)
		}
	})
	t.Run("the target keeps its machine's state", func(t *testing.T) {
		if !worktreeListed(t, newHome, worktree, "after") {
			t.Fatal("the target's worktree is gone after the move")
		}
		requireStdout(t, outpost.Attn("settings", "get", "worktree_sweep_enabled"), "false")
		requireStdout(t, home.Attn("settings", "get", "worktree_sweep_enabled"), "true")
		after := analyticsRows(t, newHome)
		for _, row := range analyticsBefore {
			if !slices.ContainsFunc(after, func(r analytics.Record) bool {
				return strings.HasPrefix(string(r.Stream), string(row.Stream)+"_m") && r.Seq == row.Seq
			}) {
				t.Fatalf("the target's analytics record %s/%d is not at the new home under the target's stream", row.Stream, row.Seq)
			}
		}
	})
	t.Run("the home's unsent analytics move with its analytics token", func(t *testing.T) {
		if len(homeAnalytics) == 0 {
			t.Fatal("the home recorded no analytics before the move")
		}
		after := analyticsRows(t, outpost.App())
		for _, row := range homeAnalytics {
			if !slices.ContainsFunc(after, func(r analytics.Record) bool { return r.Stream == row.Stream && r.Seq == row.Seq }) {
				t.Fatalf("the home's analytics record %s/%d is gone after the move", row.Stream, row.Seq)
			}
		}
		for _, row := range analyticsRows(t, home.App()) {
			if slices.ContainsFunc(homeAnalytics, func(r analytics.Record) bool { return r.Stream == row.Stream && r.Seq == row.Seq && r.At.Equal(row.At) }) {
				t.Fatalf("the old home keeps analytics record %s/%d, which the new home now ships", row.Stream, row.Seq)
			}
		}
	})
	t.Run("crew homes in the Notebook follow it to its copy on the new home", func(t *testing.T) {
		if len(move.Remaps) != 1 {
			t.Fatalf("remaps: %+v", move.Remaps)
		}
		var members []protocol.CrewMember
		outpost.Attn("crew", "list", "--json").JSON(t, &members)
		for _, member := range members {
			if !strings.HasPrefix(member.HomeDir, move.Remaps[0].To+string(filepath.Separator)) {
				t.Errorf("%s's home is %s, outside the new home's Notebook %s", member.Name, member.HomeDir, move.Remaps[0].To)
			}
		}
		if len(members) == 0 {
			t.Fatal("the new home lists no crew member")
		}
	})
	t.Run("no machine changes its identity or renames a Notebook copy", func(t *testing.T) {
		for s, want := range identities {
			if got := identityFiles(t, s); !maps.Equal(got, want) {
				t.Errorf("identity files of %s changed", s.Dir)
			}
			if got := notebookDirs(t, s); !slices.Equal(got, notebooks[s]) {
				t.Errorf("Notebook folders of %s: %v, were %v", s.Dir, got, notebooks[s])
			}
		}
	})
}

func refusedChecks(move protocol.HomeMove) map[string]string {
	refused := map[string]string{}
	for _, check := range move.Checks {
		if !check.Ok {
			refused[check.Name] = protocol.Deref(check.Detail)
		}
	}
	return refused
}

func requireRefusedBy(t *testing.T, move protocol.HomeMove, name, cause string) {
	t.Helper()
	refused := refusedChecks(move)
	if move.Step != "refused" || len(refused) != 1 || !strings.Contains(refused[name], cause) {
		t.Fatalf("want the move refused by %s naming %q: %s", name, cause, describeMove(move))
	}
}

func TestHomeMovePreflightRefusesNamingTheCause(t *testing.T) {
	home, outpost, third := testworld.NewStack(t), testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	streamOpen := home.PauseAt(pausepoint.NotebookStreamOpen)
	third.Start()
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	thirdID := pair.Enroll("third", "third", third)
	check := func() protocol.HomeMove {
		return homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome", "--check")
	}

	t.Run("the Notebook is not in sync", func(t *testing.T) {
		streamOpen.Await()
		requireRefusedBy(t, check(), "notebook", "wait until it is in sync")
		streamOpen.Release()
		awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)
		awaitMachineNotebook(t, pair.App, thirdID, protocol.OutpostNotebookStateInSync)
		if move := check(); move.Step != "checked" {
			t.Fatalf("with everything healthy: %s", describeMove(move))
		}
	})
	tools := filepath.Join(outpost.Dir, "machine-bin")
	swapTool := func(t *testing.T, name, body string) {
		t.Helper()
		path := filepath.Join(tools, name)
		healthy, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(path, healthy, 0o700) })
	}
	t.Run("gh is not logged in on the target", func(t *testing.T) {
		swapTool(t, "gh", "echo 'You are not logged into any GitHub hosts. To log in, run: gh auth login' >&2; exit 1")
		requireRefusedBy(t, check(), "gh", "not logged into any GitHub hosts")
	})
	t.Run("linger is off on the target", func(t *testing.T) {
		swapTool(t, "loginctl", "echo no")
		requireRefusedBy(t, check(), "linger", "loginctl enable-linger")
	})
	t.Run("the target cannot reach the third outpost", func(t *testing.T) {
		pair.SSH.Fail("third", "ssh: connect to host third port 22: No route to host")
		defer pair.SSH.Restore("third")
		requireRefusedBy(t, check(), "ssh third", "No route to host")
	})
}

func newCrashingPair(t *testing.T, homeCrash, targetCrash string) *movingPair {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{home, outpost} {
		s.Vars = append(s.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	startWith := func(s *testworld.Stack, point string) {
		if point == "" {
			s.Start()
		} else {
			s.StartCrashingAt(point)
		}
	}
	startWith(outpost, targetCrash)
	startWith(home, homeCrash)
	pair.Connect()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	m := &movingPair{OutpostPair: pair, t: t, shellH: fakeagent.Harness(protocol.SessionAgentShell)}
	m.onHome = home.Spawn(pair.App, m.shellH, home.Path("shell"))
	m.onOut = home.Spawn(pair.App, m.shellH, outpost.Path("shell"), func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	m.pidHome, m.pidOut = home.Path("h.pid"), outpost.Path("o.pid")
	m.echo(pair.App, "before")
	awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)
	return m
}

// restartAfterRollback starts both machines after the old home rolled back a move whose target crashed while it
// applied, and lets the target learn of the rollback from its own dial to the old home. An ssh dial starts the
// dialed machine's daemon, so both routes stay failed while the test starts each daemon itself.
func (m *movingPair) restartAfterRollback() {
	m.t.Helper()
	m.Home.AwaitStopped()
	m.SSH.Fail(m.Destination, "ssh: connect to host outpost port 22: No route to host")
	m.SSH.Fail("oldhome", "ssh: connect to host oldhome port 22: No route to host")
	m.Outpost.Start()
	m.Home.Start()
	m.SSH.Restore("oldhome")
	redial(m.Outpost)
	m.Outpost.AwaitStopped()
	m.Outpost.Start()
	m.SSH.Restore(m.Destination)
}

// redial fires the retry of the dial that s's daemon made while the other machine refused it, holding the home itself.
func homeUnit(s *testworld.Stack) string {
	return filepath.Join(s.Path("xdg-config"), "systemd", "user", "attn.service")
}

func redial(s *testworld.Stack) {
	s.T.Helper()
	s.AwaitTimer(bridge.RetryTimer)
	s.FirePending(bridge.RetryTimer)
}

func (m *movingPair) requireRoles(t *testing.T, homeIsHome bool) {
	t.Helper()
	if role(t, m.Home).IsHome() != homeIsHome || role(t, m.Outpost).IsHome() == homeIsHome {
		t.Fatalf("roles: home %+v, target %+v; want the first machine home: %v", role(t, m.Home), role(t, m.Outpost), homeIsHome)
	}
}

func (m *movingPair) requireShellsKept(t *testing.T, app *testworld.Peer, tag, pidHome, pidOut string) {
	t.Helper()
	if a, b := m.echo(app, tag); a != pidHome || b != pidOut {
		t.Fatalf("pids: %s->%s, %s->%s", pidHome, a, pidOut, b)
	}
}

func (m *movingPair) requireMoved(t *testing.T, homeMachine, pidHome, pidOut string) {
	t.Helper()
	m.requireRoles(t, false)
	requireUnitEnabled(t, m.Outpost)
	app := m.Outpost.App()
	awaitConnected(t, app, homeMachine)
	m.requireShellsKept(t, app, "moved", pidHome, pidOut)
}

func TestHomeMoveSurvivesAKillAtEachStep(t *testing.T) {
	before := []struct{ name, home, target string }{
		{"the home after it fenced", "home-move-fenced", ""},
		{"the home after the target staged", "home-move-staged", ""},
	}
	for _, kill := range before {
		t.Run("before commit: "+kill.name, func(t *testing.T) {
			m := newCrashingPair(t, kill.home, kill.target)
			homeMachine := role(t, m.Home).MachineID
			pidHome, pidOut := m.echo(m.App, "start")
			_ = m.Home.Attn("home", "move", "outpost", "--via", "oldhome")
			m.Home.AwaitCrash()
			m.Home.Start()
			m.requireRoles(t, true)
			app := m.Home.App()
			awaitConnected(t, app, m.Host)
			m.requireShellsKept(t, app, "after", pidHome, pidOut)
			awaitMachineNotebook(t, app, m.Host, protocol.OutpostNotebookStateInSync)
			if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
				t.Fatalf("a new move after the kill: %s", describeMove(move))
			}
			m.Home.AwaitStopped()
			m.Outpost.AwaitStopped()
			m.Home.Start()
			m.Outpost.Start()
			m.requireMoved(t, homeMachine, pidHome, pidOut)
		})
	}
	during := []struct{ name, home, target string }{
		{"the home as it commits", "home-move-committing", ""},
		{"the target as it commits", "", "home-move-target-committing"},
		{"the home after the target committed", "home-move-target-confirmed", ""},
	}
	for _, kill := range during {
		t.Run("during commit: "+kill.name, func(t *testing.T) {
			m := newCrashingPair(t, kill.home, kill.target)
			homeMachine := role(t, m.Home).MachineID
			pidHome, pidOut := m.echo(m.App, "start")
			_ = m.Home.Attn("home", "move", "outpost", "--via", "oldhome")
			killed, other := m.Home, m.Outpost
			if kill.target != "" {
				killed, other = m.Outpost, m.Home
			}
			killed.AwaitCrash()
			if kill.name == "the home after the target committed" {
				other.AwaitStopped()
				m.SSH.Fail("oldhome", "ssh: connect to host oldhome port 22: No route to host")
				other.Start()
				killed.Start()
				m.SSH.Restore("oldhome")
				redial(m.Outpost)
				m.Home.AwaitStopped()
				m.SSH.Fail("oldhome", "ssh: connect to host oldhome port 22: No route to host")
				m.Home.Start()
				m.SSH.Restore("oldhome")
				redial(m.Outpost)
				m.requireMoved(t, homeMachine, pidHome, pidOut)
				return
			}
			killed.Start()
			awaitOutpost(t, m.Home.App(), m.Host, func(row protocol.OutpostInfo) bool {
				return row.State == protocol.OutpostStateConnected || strings.Contains(protocol.Deref(row.LastError), "since move")
			})
			if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
				t.Fatalf("running the move again: %s", describeMove(move))
			}
			m.Home.AwaitStopped()
			m.Outpost.AwaitStopped()
			m.Outpost.Start()
			m.Home.Start()
			m.requireMoved(t, homeMachine, pidHome, pidOut)
		})
	}
	applying := []string{"home-move-apply-database", "home-move-apply-enrollment", "home-move-apply-files", "home-move-apply-roots"}
	for _, point := range applying {
		t.Run("while each machine applies: "+point, func(t *testing.T) {
			m := newCrashingPair(t, "", "")
			homeMachine := role(t, m.Home).MachineID
			pidHome, pidOut := m.echo(m.App, "start")
			attachment := filepath.Join("attachments", "only-on-the-home.txt")
			if err := os.MkdirAll(filepath.Join(m.Home.Dir, "attachments"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(m.Home.Dir, attachment), []byte("kept"), 0o600); err != nil {
				t.Fatal(err)
			}
			if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
				t.Fatalf("move: %s", describeMove(move))
			}
			m.Home.AwaitStopped()
			m.Outpost.AwaitStopped()
			for _, s := range []*testworld.Stack{m.Home, m.Outpost} {
				s.CrashWhileStartingAt(point)
				s.Start()
			}
			m.requireMoved(t, homeMachine, pidHome, pidOut)
			if data, err := os.ReadFile(filepath.Join(m.Outpost.Dir, attachment)); string(data) != "kept" {
				t.Fatalf("an attachment the target lacked, on the new home after a retried apply: %q, %v", data, err)
			}
		})
	}
	t.Run("rollback after the new home failed to start", func(t *testing.T) {
		m := newCrashingPair(t, "", "")
		pidHome, pidOut := m.echo(m.App, "start")
		unit := homeUnit(m.Home)
		if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
			t.Fatalf("move: %s", describeMove(move))
		}
		m.Home.AwaitStopped()
		m.Outpost.AwaitStopped()
		m.Outpost.CrashWhileStartingAt("home-move-apply-files")
		m.Home.Start()
		if back := homeMoveJSON(t, m.Home, "rollback"); back.Step != "rolling-back" {
			t.Fatalf("rollback: %s", describeMove(back))
		}
		m.restartAfterRollback()
		defer m.Home.FireTimers(bridge.RetryTimer)()
		m.requireRoles(t, true)
		app := m.Home.App()
		awaitConnected(t, app, m.Host)
		m.requireShellsKept(t, app, "rolled-back", pidHome, pidOut)
		if _, err := os.Stat(unit); err != nil {
			t.Fatalf("the unit that starts the home is gone after the rollback: %v", err)
		}
	})
	t.Run("a machine setting the target took after it staged survives the move", func(t *testing.T) {
		m := newCrashingPair(t, "home-move-committing", "")
		homeMachine := role(t, m.Home).MachineID
		pidHome, pidOut := m.echo(m.App, "start")
		_ = m.Home.Attn("home", "move", "outpost", "--via", "oldhome")
		m.Home.AwaitCrash()
		requireStdout(t, m.Outpost.Attn("settings", "set", "worktree_sweep_enabled", "false"), "false")
		m.Home.Start()
		awaitOutpost(t, m.Home.App(), m.Host, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
		if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
			t.Fatalf("finishing the move: %s", describeMove(move))
		}
		m.Home.AwaitStopped()
		m.Outpost.AwaitStopped()
		m.Home.Start()
		m.Outpost.Start()
		m.requireMoved(t, homeMachine, pidHome, pidOut)
		requireStdout(t, m.Outpost.Attn("settings", "get", "worktree_sweep_enabled"), "false")
	})
	t.Run("a check while the move commits changes nothing", func(t *testing.T) {
		m := newCrashingPair(t, "home-move-committing", "")
		_ = m.Home.Attn("home", "move", "outpost", "--via", "oldhome")
		m.Home.AwaitCrash()
		m.Home.Start()
		awaitOutpost(t, m.Home.App(), m.Host, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
		if check := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome", "--check"); check.Step != "committing" {
			t.Fatalf("a check of the committing move: %s", describeMove(check))
		}
		if status := homeMoveJSON(t, m.Home, "status"); status.Step != "committing" {
			t.Fatalf("the move after its check: %s", describeMove(status))
		}
		m.requireRoles(t, true)
	})
	t.Run("rollback after the target died as it committed leaves it no unit", func(t *testing.T) {
		m := newCrashingPair(t, "", "home-move-target-committing")
		_ = m.Home.Attn("home", "move", "outpost", "--via", "oldhome")
		m.Outpost.AwaitCrash()
		m.Outpost.Start()
		awaitOutpost(t, m.Home.App(), m.Host, func(row protocol.OutpostInfo) bool { return row.State == protocol.OutpostStateConnected })
		if back := homeMoveJSON(t, m.Home, "rollback"); back.Step != "aborted" {
			t.Fatalf("rollback: %s", describeMove(back))
		}
		if _, err := os.Stat(homeUnit(m.Outpost)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the target that never committed has a unit that starts it as the home: %v", err)
		}
		m.requireRoles(t, true)
	})
	t.Run("rollback from the app after the new home failed to start", func(t *testing.T) {
		m := newCrashingPair(t, "", "")
		pidHome, pidOut := m.echo(m.App, "start")
		if move := homeMoveJSON(t, m.Home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
			t.Fatalf("move: %s", describeMove(move))
		}
		m.Home.AwaitStopped()
		m.Outpost.AwaitStopped()
		m.Outpost.CrashWhileStartingAt("home-move-apply-files")
		m.Home.Start()
		app := m.Home.App()
		ask := func(id string, message any) protocol.HomeMove {
			result := testworld.Request(app, message, protocol.EventHomeMoveResult, func(r protocol.HomeMoveResultMessage) bool { return r.RequestID == id })
			if !result.Success || result.HomeMove == nil {
				t.Fatalf("%s: %+v", id, result)
			}
			return *result.HomeMove
		}
		latest := ask("status", protocol.HomeMoveStatusMessage{Cmd: protocol.CmdHomeMoveStatus, RequestID: protocol.Ptr("status")})
		if protocol.Deref(latest.Side) != "from" || latest.Step != "applied" || protocol.Deref(latest.To) != "outpost" {
			t.Fatalf("the app reads the old home's move as %s, want side from, step applied, to outpost", describeMove(latest))
		}
		if back := ask("rollback", protocol.HomeMoveRollbackMessage{Cmd: protocol.CmdHomeMoveRollback, RequestID: protocol.Ptr("rollback")}); back.Step != "rolling-back" {
			t.Fatalf("rollback from the app: %s", describeMove(back))
		}
		m.restartAfterRollback()
		defer m.Home.FireTimers(bridge.RetryTimer)()
		m.requireRoles(t, true)
		app = m.Home.App()
		awaitConnected(t, app, m.Host)
		m.requireShellsKept(t, app, "rolled-back", pidHome, pidOut)
	})
}

func TestHomeMoveToAMachineThatDialsTheHome(t *testing.T) {
	healthy := func(w *reverseWorld) {
		healthyMachine(t, w.home)
		healthyMachine(t, w.laptop)
	}
	joined := func(t *testing.T) (*reverseWorld, string, string) {
		w := newReverseWorldWith(t, healthy)
		laptop := w.join().ID
		awaitMachineNotebook(t, w.app, laptop, protocol.OutpostNotebookStateInSync)
		return w, role(t, w.home).MachineID, laptop
	}
	t.Run("a home killed after the target committed finishes the move when the target dials it", func(t *testing.T) {
		w, homeMachine, laptop := joined(t)
		w.home.Stop()
		w.home.StartCrashingAt("home-move-target-confirmed")
		redial(w.laptop)
		app := w.home.App()
		awaitConnected(t, app, laptop)
		awaitMachineNotebook(t, app, laptop, protocol.OutpostNotebookStateInSync)
		_ = w.home.Attn("home", "move", w.name, "--via", "home")
		w.home.AwaitCrash()
		w.laptop.AwaitStopped()
		w.ssh.Fail("home", "ssh: connect to host home port 22: No route to host")
		w.laptop.Start()
		w.home.Start()
		w.ssh.Restore("home")
		redial(w.laptop)
		w.home.AwaitStopped()
		w.ssh.Fail("home", "ssh: connect to host home port 22: No route to host")
		w.home.Start()
		w.ssh.Restore("home")
		redial(w.laptop)
		if !role(t, w.laptop).IsHome() || role(t, w.home).IsHome() {
			t.Fatalf("roles: old home %+v, target %+v; want the target home", role(t, w.home), role(t, w.laptop))
		}
		awaitConnected(t, w.laptop.App(), homeMachine)
		if done := homeMoveJSON(t, w.home, "status"); done.Step != "done" {
			t.Fatalf("the old home's move once the new home bridged: %s", describeMove(done))
		}
	})
	t.Run("a machine that dials the old home is told to join the new one, and joins it", func(t *testing.T) {
		w := newReverseWorldWith(t, healthy)
		third := testworld.NewStack(t)
		w.ssh.Use(third.World)
		third.Start()
		requireStdout(t, third.Attn("settings", "set", "remote_home_enabled", "true"), "true")
		requireStdout(t, third.Attn("enrollment", "join", "home"), "dial:       home (bridged)")
		thirdMachine := role(t, third).MachineID
		awaitMachineNotebook(t, w.app, thirdMachine, protocol.OutpostNotebookStateInSync)
		laptop := w.join().ID
		awaitMachineNotebook(t, w.app, laptop, protocol.OutpostNotebookStateInSync)
		if move := homeMoveJSON(t, w.home, "move", laptop, "--via", "home"); move.Step != "committed" {
			t.Fatalf("move: %s", describeMove(move))
		}
		w.home.AwaitStopped()
		w.laptop.AwaitStopped()
		w.home.Start()
		w.laptop.Start()
		redial(third)
		third.AwaitTimer(bridge.RetryTimer)
		if status := third.Attn("enrollment", "status").Stdout; !strings.Contains(status, "attn enrollment join") || !strings.Contains(status, w.name) {
			t.Fatalf("a machine dialing the old home after the move is not told where the home went:\n%s", status)
		}
		w.ssh.Route("laptop", w.laptop)
		requireStdout(t, third.Attn("enrollment", "join", "laptop"), "dial:       laptop (bridged)")
		awaitConnected(t, w.laptop.App(), thirdMachine)
	})
	t.Run("a rollback on the old home reaches a target that took the home", func(t *testing.T) {
		w, _, laptop := joined(t)
		if move := homeMoveJSON(t, w.home, "move", w.name, "--via", "home"); move.Step != "committed" {
			t.Fatalf("move: %s", describeMove(move))
		}
		w.home.AwaitStopped()
		w.laptop.AwaitStopped()
		w.laptop.CrashWhileStartingAt("home-move-apply-files")
		w.home.Start()
		if back := homeMoveJSON(t, w.home, "rollback"); back.Step != "rolling-back" {
			t.Fatalf("rollback: %s", describeMove(back))
		}
		w.home.AwaitStopped()
		w.home.Start()
		w.laptop.StartUnprobed()
		w.laptop.AwaitStopped()
		w.laptop.Start()
		if !role(t, w.home).IsHome() || role(t, w.laptop).IsHome() {
			t.Fatalf("roles: old home %+v, target %+v; want the old home home", role(t, w.home), role(t, w.laptop))
		}
		awaitConnected(t, w.home.App(), laptop)
	})
}

func unitEnvironment(t *testing.T, unit string) []string {
	t.Helper()
	var env []string
	for _, line := range strings.Split(unit, "\n") {
		value, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			t.Fatalf("unit line %q: %v", line, err)
		}
		env = append(env, unquoted)
	}
	return env
}

func TestTheNewHomesUnitStartsItAloneAndItsAutomationsRun(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	config := outpost.Path("xdg-config")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.SSH.Use(outpost.World)
	pair.SSH.Route("oldhome", home)
	healthyMachine(t, home)
	healthyMachine(t, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "remote_home_enabled", "true"))
	spec := outpost.Path("check.yaml")
	work := outpost.Path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Check\ntrigger: {type: manual}\nprompt: Check.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", work)
	if err := os.WriteFile(spec, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var check protocol.AutomationDefinitionSummary
	home.Attn("automation", "apply", "--file", spec).JSON(t, &check)
	awaitMachineNotebook(t, pair.App, pair.Host, protocol.OutpostNotebookStateInSync)

	if move := homeMoveJSON(t, home, "move", "outpost", "--via", "oldhome"); move.Step != "committed" {
		t.Fatalf("move: %s", describeMove(move))
	}
	home.AwaitStopped()
	outpost.AwaitStopped()

	units, err := filepath.Glob(filepath.Join(config, "systemd", "user", "attn*.service"))
	if err != nil || len(units) != 1 {
		t.Fatalf("units under %s: %v %v", config, units, err)
	}
	data, err := os.ReadFile(units[0])
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{" daemon\n", "Restart=always\n", "WantedBy=default.target\n"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, unit)
		}
	}
	env := unitEnvironment(t, unit)
	if !slices.Contains(env, "ATTN_DATA_DIR="+outpost.Dir) {
		t.Fatalf("unit environment %v does not name the data folder %s", env, outpost.Dir)
	}
	name := filepath.Base(units[0])
	calls, err := os.ReadFile(filepath.Join(outpost.Dir, "systemctl.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--user enable " + name, "--user start --no-block " + name} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("systemctl was not asked %q:\n%s", want, calls)
		}
	}

	pair.SSH.Fail("oldhome", "ssh: connect to host oldhome port 22: No route to host")
	outpost.Vars = append(outpost.Vars, env...)
	outpost.Start()
	if !role(t, outpost).IsHome() {
		t.Fatalf("the unit started an outpost: %+v", role(t, outpost))
	}
	requireStdout(t, outpost.Attn("automation", "run", strconv.Itoa(check.ID)))
	outpost.AwaitJobsSettled()
	var runs []protocol.AutomationRunSummary
	outpost.Attn("automation", "runs", strconv.Itoa(check.ID), "--json").JSON(t, &runs)
	if len(runs) != 1 || runs[0].State != "delivered" {
		t.Fatalf("the new home did not deliver a run of the moved automation: %+v", runs)
	}
}

func TestAnAppAwayFromItsHomeReadsAndRollsBackThisMachinesMove(t *testing.T) {
	w := newReverseWorld(t)
	w.join()
	w.laptop.App()
	w.home.Stop()
	w.laptop.AwaitTimer(bridge.RetryTimer)
	away := awaitHomeDial(t, w.laptop, "dialing")
	answer := func(id string, message any) protocol.HomeMoveResultMessage {
		return testworld.Request(away, message, protocol.EventHomeMoveResult, func(r protocol.HomeMoveResultMessage) bool { return r.RequestID == id })
	}
	status := answer("status", protocol.HomeMoveStatusMessage{Cmd: protocol.CmdHomeMoveStatus, RequestID: protocol.Ptr("status")})
	if !status.Success || status.HomeMove == nil || status.HomeMove.Step != "none" {
		t.Fatalf("home_move_status away from the home: %+v", status)
	}
	rollback := answer("rollback", protocol.HomeMoveRollbackMessage{Cmd: protocol.CmdHomeMoveRollback, RequestID: protocol.Ptr("rollback")})
	if rollback.Success || !strings.Contains(protocol.Deref(rollback.Error), "no move to roll back") {
		t.Fatalf("home_move_rollback with no move, away from the home: %+v", rollback)
	}
}
