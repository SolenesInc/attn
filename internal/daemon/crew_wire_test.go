package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var crewHomeLetters = map[string]string{
	"alder":   "2026-08-10T19-20Z-alder.md",
	"keel":    "2026-08-13T22-10Z-keel.md",
	"trellis": "2026-08-13T22-20Z-trellis.md",
}

func newCrewWorld(t *testing.T, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := &world{World: prepareWorld(t, agents...)}
	for member, letter := range crewHomeLetters {
		writeCrewHomeFile(t, w, member, crew.CharterFileName, "# "+member+"\n\nWhat I care about.\n")
		writeCrewHomeFile(t, w, member, filepath.Join(crew.HandoffsDirName, letter), "Where I left off.\n")
	}
	w.start()
	return w
}

func crewHome(w *world, member string) string {
	return filepath.Join(w.Dir, crew.HomesDirName, member)
}

func writeCrewHomeFile(t *testing.T, w *world, member, name, body string) string {
	t.Helper()
	path := filepath.Join(crewHome(w, member), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func crewRoster(t *testing.T, cli *client.Client) []protocol.CrewMember {
	t.Helper()
	roster, err := cli.CrewList()
	if err != nil {
		t.Fatalf("crew list: %v", err)
	}
	return roster.Members
}

func crewRosterMember(t *testing.T, cli *client.Client, id string) protocol.CrewMember {
	t.Helper()
	members := crewRoster(t, cli)
	index := slices.IndexFunc(members, func(m protocol.CrewMember) bool { return m.ID == id })
	if index < 0 {
		t.Fatalf("no member %q in the roster", id)
	}
	return members[index]
}

func wakeCrew(t *testing.T, cli *client.Client, member, agent string) *protocol.CrewWakeResult {
	t.Helper()
	woken, err := cli.CrewWake(member, agent)
	if err != nil {
		t.Fatalf("wake %s: %v", member, err)
	}
	return woken
}

func setCrew(t *testing.T, cli *client.Client, member string, change protocol.CrewSetMessage) protocol.CrewMember {
	t.Helper()
	set, err := cli.CrewSet(member, change.Cwd, change.Agent, change.Model, change.Effort, change.AwarenessDirs)
	if err != nil {
		t.Fatalf("crew set %s: %v", member, err)
	}
	return set.Member
}

func crewSessionCount(t *testing.T, cli *client.Client) int {
	t.Helper()
	list, err := cli.List("")
	if err != nil {
		t.Fatal(err)
	}
	return len(list.Sessions)
}

func crewErrorContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("succeeded, want a refusal naming %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func crewLaunchFlag(argv []string, flag string) string {
	index := slices.Index(argv, flag)
	if index < 0 || index+1 >= len(argv) {
		return ""
	}
	return argv[index+1]
}

func TestWakingAMemberStartsOneDayWhereItWorksAndBindsItOnTheRoster(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	workDir := w.Path("trellis-work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "trellis", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir)})

	woken := wakeCrew(t, cli, "Trellis", "")
	if woken.Member != "trellis" || woken.AlreadyAwake || woken.WorkspaceID != "workspace-crew-trellis" {
		t.Fatalf("wake = %+v, want a fresh trellis day in workspace-crew-trellis", woken)
	}
	w.Launched(woken.SessionID)
	session := testworld.AwaitSession(app, woken.SessionID, func(s protocol.Session) bool { return protocol.Deref(s.CrewMember) == "trellis" })
	if session.Directory != workDir || session.Label != "Trellis" || session.WorkspaceID != woken.WorkspaceID {
		t.Errorf("day = dir %q label %q workspace %q, want %q, Trellis, %q", session.Directory, session.Label, session.WorkspaceID, workDir, woken.WorkspaceID)
	}
	workspace := testworld.Await(app, protocol.EventWorkspaceRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Workspace != nil && e.Workspace.ID == woken.WorkspaceID
	})
	if workspace.Workspace.Title != "Trellis" {
		t.Errorf("workspace title = %q, want Trellis", workspace.Workspace.Title)
	}
	testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		return slices.ContainsFunc(e.Members, func(m protocol.CrewMember) bool {
			return m.ID == "trellis" && protocol.Deref(m.BindingSession) == woken.SessionID
		})
	})
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != woken.SessionID {
		t.Fatalf("roster binding = %q, want %q", got, woken.SessionID)
	}

	again := wakeCrew(t, cli, "trellis", "")
	if !again.AlreadyAwake || again.SessionID != woken.SessionID {
		t.Fatalf("second wake = %+v, want the live day %s", again, woken.SessionID)
	}

	keel := wakeCrew(t, cli, "keel", "")
	w.Launched(keel.SessionID)
	homeDay := testworld.AwaitSession(app, keel.SessionID, func(s protocol.Session) bool { return s.Directory != "" })
	if homeDay.Directory != crewHome(w, "keel") {
		t.Errorf("a member with no recorded cwd launched in %q, want its home %q", homeDay.Directory, crewHome(w, "keel"))
	}

	if got := crewSessionCount(t, cli); got != 2 {
		t.Errorf("sessions = %d, want one day per woken member", got)
	}
}

func TestCrewHomesJoinTheRosterAsleepAndAClosedDayReleasesItsMember(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	members := crewRoster(t, cli)
	if len(members) != 3 {
		t.Fatalf("roster = %d members, want the 3 homes on disk", len(members))
	}
	for _, member := range members {
		if member.BindingSession != nil {
			t.Errorf("%s was imported awake in %s", member.ID, *member.BindingSession)
		}
		if _, err := os.Stat(member.CharterPath); err != nil {
			t.Errorf("%s's charter path does not point at its charter: %v", member.ID, err)
		}
	}

	woken := wakeCrew(t, cli, "trellis", "")
	w.Launched(woken.SessionID)
	peek, err := cli.AgentPeek(woken.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := protocol.Deref(peek.CrewMember); got != "trellis" {
		t.Fatalf("peek names crew member %q, want trellis", got)
	}

	if err := cli.Unregister(woken.SessionID); err != nil {
		t.Fatal(err)
	}
	if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
		t.Fatalf("the binding survived its closed session: %s", *binding)
	}
}

func TestAMembersDayThatEndsIsReleasedAndTheNextWakeStartsFresh(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()

	first := wakeCrew(t, cli, "alder", "")
	w.Launched(first.SessionID).Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == first.SessionID })
	if binding := crewRosterMember(t, cli, "alder").BindingSession; binding != nil {
		t.Fatalf("the exited day still holds alder: %s", *binding)
	}
	list, err := cli.List("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list.Sessions, func(s protocol.Session) bool { return s.ID == first.SessionID }) {
		t.Error("releasing the member erased the exited day's session")
	}

	second := wakeCrew(t, cli, "alder", "")
	if second.AlreadyAwake || second.SessionID == first.SessionID || protocol.Deref(second.ReleasedSessionID) != first.SessionID {
		t.Fatalf("wake after exit = %+v, want a fresh day naming %s released", second, first.SessionID)
	}
	day := w.Launched(second.SessionID)
	day.Prompted()
	day.Reply("Picked up where I left off. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, second.SessionID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	w.restart()
	app = w.App()
	cli = w.Client()
	if !slices.ContainsFunc(app.Initial.Sessions, func(s protocol.Session) bool {
		return s.ID == second.SessionID && s.State == protocol.SessionStateRecoverable
	}) {
		t.Fatalf("the day that died with the daemon did not come back recoverable: %+v", app.Initial.Sessions)
	}
	if binding := crewRosterMember(t, cli, "alder").BindingSession; binding != nil {
		t.Fatalf("the recoverable day still holds alder after the restart: %s", *binding)
	}
	third := wakeCrew(t, cli, "alder", "")
	w.Launched(third.SessionID)
	if protocol.Deref(third.ReleasedSessionID) != second.SessionID {
		t.Fatalf("wake after the restart released %q, want %s", protocol.Deref(third.ReleasedSessionID), second.SessionID)
	}
}

func TestConcurrentWakesOfOneMemberShareOneDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	results := make(chan *protocol.CrewWakeResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			woken, err := w.Client().CrewWake("keel", "")
			if err != nil {
				failures <- err
				return
			}
			results <- woken
		}()
	}
	var wakes []*protocol.CrewWakeResult
	for range 2 {
		select {
		case err := <-failures:
			t.Fatalf("concurrent wake: %v", err)
		case woken := <-results:
			wakes = append(wakes, woken)
		}
	}
	if wakes[0].SessionID != wakes[1].SessionID || wakes[0].AlreadyAwake == wakes[1].AlreadyAwake {
		t.Fatalf("concurrent wakes = %+v and %+v, want one launch and one answer naming the same day", wakes[0], wakes[1])
	}
	w.Launched(wakes[0].SessionID)
	if got := crewSessionCount(t, w.Client()); got != 1 {
		t.Fatalf("concurrent wakes left %d sessions, want one", got)
	}
}

func TestCrewWakeAndSetRefusalsNameWhatToDo(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	_, err := cli.CrewWake("nobody", "")
	crewErrorContains(t, err, "attn crew list")

	moved := w.Path("moved")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "alder", protocol.CrewSetMessage{Cwd: protocol.Ptr(moved)})
	if err := os.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
	_, err = cli.CrewWake("alder", "")
	crewErrorContains(t, err, "Alder launches in", moved, "attn crew set alder --cwd")
	if binding := crewRosterMember(t, cli, "alder").BindingSession; binding != nil {
		t.Errorf("a refused wake left alder bound to %s", *binding)
	}
	if got := crewSessionCount(t, cli); got != 0 {
		t.Errorf("a refused wake started %d sessions", got)
	}

	missing := w.Path("not-there")
	_, err = cli.CrewSet("keel", protocol.Ptr(missing), nil, nil, nil, nil)
	crewErrorContains(t, err, missing)

	pinned := setCrew(t, cli, "trellis", protocol.CrewSetMessage{Effort: protocol.Ptr("high")})
	_, err = cli.CrewSet("trellis", nil, protocol.Ptr("nosuchharness"), protocol.Ptr("some-model"), protocol.Ptr("max"), nil)
	crewErrorContains(t, err, "nosuchharness")
	after := crewRosterMember(t, cli, "trellis")
	if after.Revision != pinned.Revision || after.Agent != nil || after.Model != nil || protocol.Deref(after.Effort) != "high" {
		t.Fatalf("a refused selection changed trellis: before %+v, after %+v", pinned, after)
	}
}

func TestCrewSetRecordsAndClearsEachFieldIndependently(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	workDir, awareness := w.Path("keel-work"), w.Path("keel-notes")
	for _, dir := range []string{workDir, awareness} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	set := setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir), AwarenessDirs: []string{awareness}})
	if protocol.Deref(set.Cwd) != workDir || !slices.Equal(set.AwarenessDirs, []string{awareness}) {
		t.Fatalf("set = cwd %q dirs %q, want %q and [%q]", protocol.Deref(set.Cwd), set.AwarenessDirs, workDir, awareness)
	}
	setCrew(t, cli, "keel", protocol.CrewSetMessage{AwarenessDirs: []string{}})
	keel := crewRosterMember(t, cli, "keel")
	if len(keel.AwarenessDirs) != 0 || protocol.Deref(keel.Cwd) != workDir {
		t.Fatalf("after clearing awareness dirs keel has cwd %q dirs %q, want %q and none", protocol.Deref(keel.Cwd), keel.AwarenessDirs, workDir)
	}

	setCrew(t, cli, "keel", protocol.CrewSetMessage{Effort: protocol.Ptr("high")})
	switched := setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
	if protocol.Deref(switched.Agent) != "codex" || switched.Effort != nil {
		t.Fatalf("switching harness left agent %q effort %q, want codex with its pins cleared", protocol.Deref(switched.Agent), protocol.Deref(switched.Effort))
	}
	cleared := setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("")})
	if cleared.Agent != nil || cleared.ResolvedAgent != crew.DefaultAgent {
		t.Fatalf("clearing the agent left %v resolving to %q, want unset resolving to %q", cleared.Agent, cleared.ResolvedAgent, crew.DefaultAgent)
	}
	if got := crewRosterMember(t, cli, "keel"); got.Agent != nil || protocol.Deref(got.Cwd) != workDir {
		t.Fatalf("roster keel = agent %v cwd %q, want unset and %q", got.Agent, protocol.Deref(got.Cwd), workDir)
	}
}

func TestAWakeLaunchesTheMembersHarnessAndEffort(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	cli := w.Client()
	type launch struct {
		harness                 fakeagent.Harness
		model, effort           string
		registeredAgent, roster string
	}
	wake := func(step, member, flag string, want launch) {
		t.Helper()
		woken := wakeCrew(t, cli, member, flag)
		run := w.Launched(woken.SessionID)
		if run.Harness != want.harness {
			t.Errorf("%s: woke on %s, want %s", step, run.Harness, want.harness)
		}
		if got := crewLaunchFlag(run.Argv, "--model"); got != want.model {
			t.Errorf("%s: launched with --model %q, want %q (argv %q)", step, got, want.model, run.Argv)
		}
		if got := crewLaunchFlag(run.Argv, "--effort"); got != want.effort {
			t.Errorf("%s: launched with --effort %q, want %q (argv %q)", step, got, want.effort, run.Argv)
		}
		registered := crewRosterMember(t, cli, member)
		if got := protocol.Deref(registered.Agent); got != want.registeredAgent {
			t.Errorf("%s: roster agent = %q, want %q", step, got, want.registeredAgent)
		}
		if got := protocol.Deref(registered.ResolvedModel); got != want.roster {
			t.Errorf("%s: roster resolved model = %q, want %q", step, got, want.roster)
		}
		if err := cli.Unregister(woken.SessionID); err != nil {
			t.Fatal(err)
		}
	}

	wake("an unset agent wakes on the default harness and its historical model", "trellis", "",
		launch{harness: fakeagent.Claude, model: "fable", roster: "fable"})

	setSetting(t, app, "default_model_claude", "claude-opus-4-1")
	wake("the harness's configured default model wins over the historical one", "trellis", "",
		launch{harness: fakeagent.Claude, model: "claude-opus-4-1", roster: "claude-opus-4-1"})

	setCrew(t, cli, "alder", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
	wake("a member registered on codex wakes on codex unpinned", "alder", "",
		launch{harness: fakeagent.Codex, registeredAgent: "codex"})
	wake("the flag wins for one day without moving the member", "alder", "claude",
		launch{harness: fakeagent.Claude, model: "claude-opus-4-1", registeredAgent: "codex"})

	setSetting(t, app, "default_model_codex", "gpt-5.6-sol")
	wake("a one-day codex wake takes codex's configured default model", "keel", "codex",
		launch{harness: fakeagent.Codex, model: "gpt-5.6-sol", roster: "claude-opus-4-1"})

	setSetting(t, app, "default_effort_claude", "medium")
	set := setCrew(t, cli, "trellis", protocol.CrewSetMessage{Effort: protocol.Ptr("HIGH")})
	if protocol.Deref(set.Effort) != "high" || protocol.Deref(set.ResolvedEffort) != "high" {
		t.Fatalf("effort HIGH stored %q resolving to %q, want high", protocol.Deref(set.Effort), protocol.Deref(set.ResolvedEffort))
	}
	wake("a member's effort reaches the launch", "trellis", "",
		launch{harness: fakeagent.Claude, model: "claude-opus-4-1", effort: "high", roster: "claude-opus-4-1"})

	cleared := setCrew(t, cli, "trellis", protocol.CrewSetMessage{Effort: protocol.Ptr("")})
	if cleared.Effort != nil || protocol.Deref(cleared.ResolvedEffort) != "medium" {
		t.Fatalf("cleared effort = %v resolving to %q, want unset resolving to the harness default medium", cleared.Effort, protocol.Deref(cleared.ResolvedEffort))
	}
}

func TestRegisteringAsAMemberBindsOneLiveSessionPerMember(t *testing.T) {
	w := newCrewWorld(t)
	cli := w.Client()
	register := func(session, member string) error {
		return cli.RegisterAsMember(session, session, w.Path(session), "", member)
	}
	bindings := func() map[string]string {
		out := map[string]string{}
		for _, member := range crewRoster(t, cli) {
			out[member.ID] = protocol.Deref(member.BindingSession)
		}
		return out
	}

	if err := register("sess-first", "keel"); err != nil {
		t.Fatal(err)
	}
	crewErrorContains(t, register("sess-second", "Keel"), "Keel", "sess-fir")
	if err := register("sess-first", "keel"); err != nil {
		t.Fatalf("a member's own session re-announcing itself was refused: %v", err)
	}
	crewErrorContains(t, register("sess-second", "nobody"), "attn crew list")
	if err := register("sess-second", "trellis"); err != nil {
		t.Fatal(err)
	}
	crewErrorContains(t, register("sess-first", "trellis"), "Trellis")
	if got := bindings(); got["keel"] != "sess-first" || got["trellis"] != "sess-second" {
		t.Fatalf("after refused claims the roster binds %v, want keel to sess-first and trellis to sess-second", got)
	}

	if err := register("sess-first", "alder"); err != nil {
		t.Fatal(err)
	}
	if got := bindings(); got["alder"] != "sess-first" || got["keel"] != "" {
		t.Fatalf("a session taking a second name left the roster binding %v, want alder to sess-first and keel released", got)
	}
}

func TestARestartReimportsCrewHomesWithoutRewritingTheRoster(t *testing.T) {
	w := newCrewWorld(t)
	cli := w.Client()
	workDir := w.Path("keel-work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir)})

	w.restart()
	cli = w.Client()
	if got := len(crewRoster(t, cli)); got != 3 {
		t.Fatalf("roster = %d members after a restart, want 3", got)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "keel").Cwd); got != workDir {
		t.Fatalf("keel's cwd after a restart = %q, want %q", got, workDir)
	}

	writeCrewCharter(t, w, "sable")
	w.restart()
	cli = w.Client()
	if got := len(crewRoster(t, cli)); got != 4 {
		t.Fatalf("roster = %d members after a home was added by hand, want 4", got)
	}
	crewRosterMember(t, cli, "sable")
}

func TestADaemonOnACopiedDatabaseFencesAnotherInstancesCrew(t *testing.T) {
	source := newCrewWorld(t)
	source.stop()
	copied := &world{World: prepareWorld(t)}
	database, err := os.ReadFile(filepath.Join(source.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied.Dir, "attn.db"), database, 0o644); err != nil {
		t.Fatal(err)
	}
	copied.start()
	cli := copied.Client()

	foreignHomes := filepath.Join(source.Dir, crew.HomesDirName)
	_, err = cli.CrewList()
	crewErrorContains(t, err, foreignHomes, "attn.db copied from another instance")
	crewErrorContains(t, cli.RegisterAsMember("sess-copied", "sess-copied", copied.Path("sess-copied"), "", "trellis"), foreignHomes)
	if _, err := cli.List(""); err != nil {
		t.Fatalf("the daemon stopped serving sessions over a fenced crew: %v", err)
	}
}

func TestSeedTendersResolveToTheMemberTheyName(t *testing.T) {
	w := newCrewWorld(t)
	cli := w.Client()
	for _, session := range []string{"sess-keel", "sess-a", "sess-b"} {
		member := ""
		if session == "sess-keel" {
			member = "keel"
		}
		if err := cli.RegisterAsMember(session, session, w.Path(session), "", member); err != nil {
			t.Fatal(err)
		}
	}
	tend := func(session, seed, member string) (*protocol.SeedTransitionResult, error) {
		return cli.SeedTransition(session, seed, "tend", "", member, false, client.SeedTransitionOptions{})
	}

	byName := plantSeedAs(t, cli, "sess-a", "Named after a member")
	if moved, err := tend("sess-a", byName, "Keel"); err != nil || moved.Seed.TenderMember != "keel" {
		t.Fatalf("tending as Keel = %+v, %v; want the member keel", moved, err)
	}
	bySession := plantSeedAs(t, cli, "sess-a", "Tended from a member's day")
	if moved, err := tend("sess-keel", bySession, ""); err != nil || moved.Seed.TenderMember != "keel" {
		t.Fatalf("tending from keel's day = %+v, %v; want the member keel", moved, err)
	}

	unbound := plantSeedAs(t, cli, "sess-a", "Picked up by a worker")
	moved, err := tend("sess-a", unbound, "some-worker")
	if err != nil || moved.Seed.TenderMember != "some-worker" || moved.Seed.TenderSession != "" {
		t.Fatalf("tending as some-worker = %+v, %v; want the free name holding it", moved, err)
	}
	_, err = tend("sess-b", unbound, "")
	crewErrorContains(t, err, "Some-worker")
	if _, err := cli.SeedTransition("sess-a", unbound, "harvest", "done", "some-worker", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("the free name could not harvest its seed: %v", err)
	}
	if peek, err := cli.AgentPeek("sess-a"); err != nil || peek.CrewMember != nil {
		t.Fatalf("the worker's session peeks as crew member %v (%v), want none", peek, err)
	}

	held := plantSeedAs(t, cli, "sess-a", "A member's work")
	if _, err := tend("", held, "trellis"); err != nil {
		t.Fatal(err)
	}
	if _, err := tend("", held, "Trellis"); err != nil {
		t.Fatalf("trellis could not re-tend its seed as Trellis: %v", err)
	}
	_, err = tend("", held, "keel")
	crewErrorContains(t, err, "Trellis")
}

func TestAWokenMemberIsPrimedWithItsCharterLettersAndGarden(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	if err := cli.Register("planter", "planter", w.Path("planter")); err != nil {
		t.Fatal(err)
	}
	plant := func(title, partOf string) string {
		t.Helper()
		planted, err := cli.SeedPlant("planter", title, "", partOf, "", "")
		if err != nil {
			t.Fatalf("plant %q: %v", title, err)
		}
		return planted.Seed.ID
	}
	tendAs := func(seed, member string) {
		t.Helper()
		if _, err := cli.SeedTransition("", seed, "tend", "", member, false, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("%s tends %s: %v", member, seed, err)
		}
	}
	plot := plant("Finish the garden", "")
	withNote := plant("Wake priming lists the seeds a member holds", plot)
	quiet := plant("Closing a seed says what it unblocked", plot)
	alders := plant("Find seeds by keyword from the CLI", plot)
	plant("Pending decisions are a visible queue", plot)
	plant("Dropping a seed on Growing offers dispatch", plot)
	tendAs(withNote, "trellis")
	tendAs(quiet, "trellis")
	tendAs(alders, "alder")
	if _, err := cli.SeedNote("", withNote, "The tripwires are measured; the daemon adapter is next.", "trellis", "handoff", false, nil); err != nil {
		t.Fatal(err)
	}

	trellis := wakeCrew(t, cli, "trellis", "")
	w.Launched(trellis.SessionID)
	primed := crewPriming(t, cli, trellis.SessionID)
	for _, want := range []string{
		"You are **Trellis**",
		"Where I left off.",
		crewHomeLetters["trellis"],
		"## What you hold in the garden",
		"`" + withNote + "` wake-priming-lists-seeds-member-holds — Wake priming lists the seeds a member holds",
		"Freshest handoff: The tripwires are measured; the daemon adapter is next.",
		"`" + quiet + "` closing-seed-says-what-unblocked — Closing a seed says what it unblocked",
		"No handoff note yet.",
		"`" + plot + "` finish-garden — 2 ready",
	} {
		if !strings.Contains(primed, want) {
			t.Errorf("trellis's priming does not carry %q:\n%s", want, primed)
		}
	}
	if strings.Contains(primed, alders) {
		t.Error("trellis was primed with a seed alder holds")
	}

	workDir, notes := w.Path("keel-work"), w.Path("keel-notes")
	for _, dir := range []string{workDir, notes} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir), AwarenessDirs: []string{notes}})
	keel := wakeCrew(t, cli, "keel", "")
	w.Launched(keel.SessionID)
	for _, dir := range []string{workDir, notes} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	primed = crewPriming(t, cli, keel.SessionID)
	for _, want := range []string{"You hold no seeds in the garden.", workDir, notes} {
		if !strings.Contains(primed, want) {
			t.Errorf("keel's priming after its directories moved does not carry %q:\n%s", want, primed)
		}
	}

	claims := crew.MaxHeldSeeds + 3
	for i := range claims {
		tendAs(plant(fmt.Sprintf("Held seed number %d", i), ""), "alder")
	}
	alder := wakeCrew(t, cli, "alder", "")
	w.Launched(alder.SessionID)
	primed = crewPriming(t, cli, alder.SessionID)
	cut := fmt.Sprintf("You hold %d seeds and this block lists the %d you claimed most recently; `attn seed ls --flat` has them all.", claims+1, crew.MaxHeldSeeds)
	if !strings.Contains(primed, cut) {
		t.Errorf("alder's priming does not say %q:\n%s", cut, primed)
	}
	if got := strings.Count(primed, "Held seed number"); got != crew.MaxHeldSeeds {
		t.Errorf("alder's priming lists %d held seeds, want the %d the tripwire allows", got, crew.MaxHeldSeeds)
	}

	for _, session := range []string{"planter", ""} {
		nobody, err := cli.CrewPrime(session)
		if err != nil || nobody.Member != nil || nobody.Guidance != nil {
			t.Errorf("priming %q = %+v, %v; want nobody", session, nobody, err)
		}
	}
}

func crewPriming(t *testing.T, cli *client.Client, session string) string {
	t.Helper()
	primed, err := cli.CrewPrime(session)
	if err != nil {
		t.Fatalf("prime %s: %v", session, err)
	}
	return protocol.Deref(primed.Guidance)
}

func TestADefaultModelOrEffortChangeReachesTheRoster(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	for _, change := range []struct {
		key, value string
		resolved   func(protocol.CrewMember) string
	}{
		{"default_model_claude", "claude-opus-5", func(m protocol.CrewMember) string { return protocol.Deref(m.ResolvedModel) }},
		{"default_effort_claude", "high", func(m protocol.CrewMember) string { return protocol.Deref(m.ResolvedEffort) }},
	} {
		setSetting(t, app, change.key, change.value)
		testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
			return slices.ContainsFunc(e.Members, func(m protocol.CrewMember) bool { return m.ID == "trellis" && change.resolved(m) == change.value })
		})
	}
}

func TestCrewEditsFromTheAppAreRevisionChecked(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "alder")
		w.restart()
		app := w.App()
		cli := w.Client()
		save := func(requestID string, revision int, effort string) protocol.CrewSetResultMessage {
			t.Helper()
			msg := protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, Member: "alder", ExpectedRevision: protocol.Ptr(revision), Effort: protocol.Ptr(effort)}
			if requestID != "" {
				msg.RequestID = protocol.Ptr(requestID)
			}
			return testworld.Request(app, msg, protocol.EventCrewSetResult, func(r protocol.CrewSetResultMessage) bool { return r.RequestID == requestID })
		}

		read := crewRosterMember(t, cli, "alder").Revision
		saved := save("save-1", read, "high")
		if !saved.Success || saved.Member == nil || saved.Member.Revision <= read {
			t.Fatalf("save = %+v, want success past revision %d", saved, read)
		}
		stale := save("save-stale", read, "low")
		if stale.Success || !stale.Conflict || stale.Member == nil || stale.Member.Revision != saved.Member.Revision || protocol.Deref(stale.Member.Effort) != "high" {
			t.Fatalf("stale save = %+v, want a conflict carrying the saved member", stale)
		}
		unaddressed := save("", saved.Member.Revision, "max")
		if unaddressed.Success || !strings.Contains(protocol.Deref(unaddressed.Error), "request id") {
			t.Fatalf("save without a request id = %+v, want it refused", unaddressed)
		}
		if current := crewRosterMember(t, cli, "alder"); current.Revision != saved.Member.Revision || protocol.Deref(current.Effort) != "high" {
			t.Fatalf("refused saves changed alder: %+v", current)
		}

		for key, value := range map[string]string{"crew.wake_limit": "0", "crew.away_seconds": "60", "crew.heartbeat_enabled": "false", "crew.autosleep_enabled": "false"} {
			setSetting(t, app, key, value)
		}
		if err := cli.RegisterAsMember("alder-day", "alder-day", w.Path("alder-day"), "", "alder"); err != nil {
			t.Fatal(err)
		}
		app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
		w.advance(time.Minute)
		w.advance(3 * time.Minute)
		awake := crewRosterMember(t, cli, "alder").Revision
		filed := crewHandoff(t, cli, "alder-day", "Filed from the day.", false, protocol.CrewDayCloseNap)
		if !strings.Contains(protocol.Deref(filed.NapError), "crew.wake_limit=0") {
			t.Fatalf("handoff = %+v, want the nap refused at the wake limit", filed)
		}
		if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != "alder-day" {
			t.Fatalf("after the refused nap alder is bound to %q, want alder-day", got)
		}
		testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
			return slices.ContainsFunc(e.Members, func(m protocol.CrewMember) bool { return m.ID == "alder" && m.Revision > awake })
		})
		if afterLetter := save("save-after-letter", awake, "low"); !afterLetter.Conflict {
			t.Fatalf("a save carrying the revision from before the letter = %+v, want a conflict", afterLetter)
		}
	})
}
