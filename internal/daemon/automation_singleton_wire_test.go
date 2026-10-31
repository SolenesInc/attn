package daemon_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSingletonContractEditsSupersedeSessionsWithoutStoppingThem(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "*/2 * * * *", "@every 3s", 1)
	manual := regexp.MustCompile(`(?m)^trigger:.*$`).ReplaceAllString(spec, "trigger: {type: manual}")
	id := applyAutomation(t, cli, manual).ID
	initial, err := cli.AutomationRun(id, "initial-manual-run", "")
	if err != nil {
		t.Fatal(err)
	}
	legacySession := protocol.Deref(initial.Run.SessionID)
	legacy := w.Launched(string(legacySession))
	legacy.Prompted()
	legacy.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.SessionID) == legacySession })
	applyAutomation(t, cli, automationEditSpec(id, spec))
	awaitRun := func(earlier string) (run protocol.AutomationRunSummary) {
		t.Helper()
		w.tickSchedulesUntil("a new delivered run", func() bool {
			runs := automationRuns(t, cli, id)
			if len(runs) == 0 || runs[0].ID == earlier || runs[0].State != "delivered" {
				return false
			}
			run = runs[0]
			return true
		})
		return run
	}
	first := awaitRun(initial.Run.ID)
	session := protocol.Deref(first.SessionID)
	agent := w.Launched(string(session))
	agent.Prompted()
	agent.Reply("Swept. <!-- attn:state=idle -->")
	gardenNudgeMove(t, cli, string(session), protocol.Deref(first.SeedID), "harvest")
	applyAutomation(t, cli, automationEditSpec(id, strings.Replace(spec, "name: Sweep", "name: Sweep renamed", 1)))
	continued := awaitRun(first.ID)
	if protocol.Deref(continued.SessionID) != session || protocol.Deref(continued.SeedID) != protocol.Deref(first.SeedID) {
		t.Fatal("a rename or harvested seed replaced the singleton's session")
	}
	agent.Prompted()
	setAutomationEnabled(t, cli, id, false)
	updated := strings.Replace(spec, "launch: {driver: claude}", "launch: {driver: claude, effort: high}", 1)
	applyAutomation(t, cli, automationEditSpec(id, updated))
	listing, err := cli.List("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range listing.Sessions {
		if s.ID == session {
			found = true
			if s.Automation == nil || !protocol.Deref(s.Automation.Superseded) {
				t.Fatalf("released singleton still appears current: %+v", s.Automation)
			}
		}
	}
	if !found {
		t.Fatal("editing the definition removed the superseded session")
	}
	marked := testworld.AwaitSession(app, string(session), func(s protocol.Session) bool {
		return s.Automation != nil && protocol.Deref(s.Automation.Superseded)
	})
	if marked.State != "working" {
		t.Fatal("editing the definition stopped the superseded agent")
	}
	definition, err := cli.AutomationDefinition(id)
	if err != nil || definition.Definition.LastRun.Automation == nil || !protocol.Deref(definition.Definition.LastRun.Automation.Superseded) {
		t.Fatalf("definition show disagrees about the superseded last run: %+v, %v", definition, err)
	}
	agent.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.SessionID) == session })
	setAutomationEnabled(t, cli, id, true)
	var runs []protocol.AutomationRunSummary
	w.tickSchedulesUntil("a delivered run on a new session", func() bool {
		runs = automationRuns(t, cli, id)
		return len(runs) > 0 && runs[0].State == "delivered" && protocol.Deref(runs[0].SessionID) != session
	})
	setAutomationEnabled(t, cli, id, false)
	fresh := w.Launched(string(protocol.Deref(runs[0].SessionID)))
	if fresh.Resumed || !strings.Contains(fresh.Prompted(), "Sweep.") || !containsAutomationFlag(fresh.Argv, "--effort", "high") {
		t.Fatal("replacement did not start with the updated launch contract")
	}
	for _, run := range automationRuns(t, cli, id) {
		if run.Automation == nil || protocol.Deref(run.Automation.Superseded) != (protocol.Deref(run.SessionID) != protocol.Deref(runs[0].SessionID)) {
			t.Fatalf("run history disagrees about the current session: %+v", run.Automation)
		}
	}
	listing, err = cli.List("")
	if err != nil || len(listing.Sessions) != 3 {
		t.Fatalf("want three preserved sessions, got %+v, %v", listing, err)
	}
	current := 0
	for _, s := range listing.Sessions {
		if s.Automation != nil && !protocol.Deref(s.Automation.Superseded) {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("three open sessions show %d current singletons, want one", current)
	}
	t.Logf("legacy manual %s; rename and harvest reused %s; effort edit preserved its busy agent; replacement %s launched with high effort; three sessions show one current singleton", legacySession, session, protocol.Deref(runs[0].SessionID))
}

func TestAnUnresumableSingletonStartsFreshAndAClosedOneResumesItsConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	id := applyAutomation(t, cli, strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "*/2 * * * *", "@every 1s", 1)).ID
	awaitDelivered := func(earlier ...string) (run protocol.AutomationRunSummary) {
		t.Helper()
		w.tickSchedulesUntil("a new delivered run", func() bool {
			runs := automationRuns(t, cli, id)
			if len(runs) == 0 || runs[0].State != "delivered" || slices.Contains(earlier, runs[0].ID) {
				return false
			}
			run = runs[0]
			return true
		})
		setAutomationEnabled(t, cli, id, false)
		return run
	}
	first := awaitDelivered()
	lost := protocol.Deref(first.SessionID)
	agent := w.Launched(string(lost))
	agent.Prompted()
	agent.Reply("Swept. <!-- attn:state=idle -->")
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: lost})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.SessionID) == lost })
	transcripts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".claude", "projects", "*", agent.ConversationID+".jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatalf("the singleton's transcript: %v %v", transcripts, err)
	}
	if err := os.Remove(transcripts[0]); err != nil {
		t.Fatal(err)
	}

	setAutomationEnabled(t, cli, id, true)
	fresh := awaitDelivered(first.ID)
	session := protocol.Deref(fresh.SessionID)
	if session == lost || protocol.Deref(fresh.SeedID) == protocol.Deref(first.SeedID) {
		t.Fatalf("without a transcript the singleton ran on %s/%s, want a session and seed of its own", session, protocol.Deref(fresh.SeedID))
	}
	replacement := w.Launched(string(session))
	if replacement.Resumed || !strings.Contains(replacement.Prompted(), "Sweep.") {
		t.Fatalf("the replacement started as %+v, want a fresh conversation on the prompt", replacement)
	}
	for _, run := range automationRuns(t, cli, id) {
		if run.Automation == nil || protocol.Deref(run.Automation.Superseded) != (run.ID == first.ID) {
			t.Fatalf("run history disagrees about the current session: %+v", run)
		}
	}

	replacement.Reply("Swept. <!-- attn:state=idle -->")
	closeFromApp(app, string(session))
	shown, err := cli.AutomationDefinition(id)
	if err != nil || shown.Definition.LastRun.Automation == nil || protocol.Deref(shown.Definition.LastRun.Automation.Superseded) {
		t.Fatalf("after its session closed, show reads the last run as %+v (%v), want it current", shown.Definition.LastRun, err)
	}
	setAutomationEnabled(t, cli, id, true)
	reopened := awaitDelivered(first.ID, fresh.ID)
	resumed := w.Launched(string(session))
	if protocol.Deref(reopened.SessionID) != session || protocol.Deref(reopened.SeedID) != protocol.Deref(fresh.SeedID) || !resumed.Resumed || resumed.ConversationID != replacement.ConversationID {
		t.Fatalf("after closing its session the singleton ran on %s/%s as %+v, want session %s resumed on its conversation %s", protocol.Deref(reopened.SessionID), protocol.Deref(reopened.SeedID), resumed, session, replacement.ConversationID)
	}
	t.Logf("%s lost its transcript and the next run launched %s fresh; closing %s and running again resumed conversation %s", lost, session, session, replacement.ConversationID)
}

func TestEditingASingletonsContractHandsItsSeedToANewSessionAndLeavesTheOldOneWorking(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	claudeSpec := strings.Replace(automationSingletonSpec(w, "Sheriff", "latest", ""), "*/2 * * * *", "@every 3s", 1)
	codexSpec := strings.Replace(claudeSpec, "launch: {driver: claude}", "launch: {driver: codex}", 1)
	id := applyAutomation(t, cli, codexSpec).ID
	awaitDelivered := func(earlier ...string) (run protocol.AutomationRunSummary) {
		t.Helper()
		w.tickSchedulesUntil("a new delivered run", func() bool {
			runs := automationRuns(t, cli, id)
			if len(runs) == 0 || runs[0].State != "delivered" || slices.Contains(earlier, runs[0].ID) {
				return false
			}
			run = runs[0]
			return true
		})
		setAutomationEnabled(t, cli, id, false)
		return run
	}
	first := awaitDelivered()
	stake := protocol.Deref(first.SeedID)
	codexSession := protocol.Deref(first.SessionID)
	codex := w.Launched(string(codexSession))
	codex.Prompted()
	codex.Reply("Checked. <!-- attn:state=idle -->")
	setAutomationEnabled(t, cli, id, true)
	again := awaitDelivered(first.ID)
	if protocol.Deref(again.SessionID) != codexSession {
		t.Fatalf("the second codex run went to %s, want %s", protocol.Deref(again.SessionID), codexSession)
	}
	codex.Prompted()
	readInbox(t, cli, string(codexSession), 0)

	applyAutomation(t, cli, automationEditSpec(id, claudeSpec))
	setAutomationEnabled(t, cli, id, true)
	next := awaitDelivered(first.ID, again.ID)
	claudeSession := protocol.Deref(next.SessionID)
	if claudeSession == codexSession || protocol.Deref(next.SeedID) != stake {
		t.Fatalf("after the agent change the next run worked on %s/%s, want a new session continuing seed %s", claudeSession, protocol.Deref(next.SeedID), stake)
	}
	if prompt := w.Launched(string(claudeSession)).Prompted(); !strings.Contains(prompt, stake) {
		t.Fatalf("the new agent's prompt does not name its stake %s:\n%s", stake, prompt)
	}
	shown, err := cli.SeedShow("", stake)
	if err != nil {
		t.Fatal(err)
	}
	if tender := shown.Seed.Tender; tender == nil || protocol.Deref(tender.SessionID) != claudeSession || shown.Seed.Status != "growing" {
		t.Fatalf("the stake is %s with tender %+v, want growing under %s", shown.Seed.Status, shown.Seed.Tender, claudeSession)
	}
	old := testworld.AwaitSession(app, string(codexSession), func(s protocol.Session) bool {
		return s.Automation != nil && protocol.Deref(s.Automation.Superseded)
	})
	if old.State != "working" {
		t.Fatalf("the agent change left the old session %s, want it still working", old.State)
	}
	if told := inboxContents(readInbox(t, cli, string(codexSession), 0).Items); !strings.Contains(told, stake+" moved: tended") {
		t.Fatalf("the old session was not told its stake moved; its inbox holds %q", told)
	}

	applyAutomation(t, cli, automationEditSpec(id, codexSpec))
	applyAutomation(t, cli, automationEditSpec(id, strings.Replace(codexSpec, "prompt: Sweep.", "prompt: Sweep the branches.", 1)))
	setAutomationEnabled(t, cli, id, true)
	reworded := awaitDelivered(first.ID, again.ID, next.ID)
	rewordedSession := protocol.Deref(reworded.SessionID)
	if protocol.Deref(reworded.SeedID) != stake || rewordedSession == claudeSession {
		t.Fatalf("a launch edit then a prompt edit ran on %s/%s, want a new session continuing seed %s", rewordedSession, protocol.Deref(reworded.SeedID), stake)
	}
	if prompt := w.Launched(string(rewordedSession)).Prompted(); !strings.Contains(prompt, "Sweep the branches.") || !strings.Contains(prompt, stake) {
		t.Fatalf("the agent for the new prompt opened on:\n%s", prompt)
	}
	t.Logf("codex %s kept working; claude %s took over %s; after a prompt edit %s continued it", codexSession, claudeSession, stake, rewordedSession)
}

func TestASingletonRunDoesNotTakeBackASeedSomeoneElseTook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	id := applyAutomation(t, cli, strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "*/2 * * * *", "@every 1s", 1)).ID
	var first protocol.AutomationRunSummary
	w.tickSchedulesUntil("a delivered run", func() bool {
		runs := automationRuns(t, cli, id)
		if len(runs) == 0 || runs[0].State != "delivered" {
			return false
		}
		first = runs[0]
		return true
	})
	setAutomationEnabled(t, cli, id, false)
	seed := protocol.Deref(first.SeedID)
	w.Launched(string(protocol.Deref(first.SessionID))).Prompted()
	other := w.Spawn(app, fakeagent.Claude, w.Path(""))
	w.Launched(other)
	if _, err := cli.SeedTransition(protocol.SessionID(other), seed, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}

	setAutomationEnabled(t, cli, id, true)
	var refused protocol.AutomationRunSummary
	w.tickSchedulesUntil("a refused run", func() bool {
		runs := automationRuns(t, cli, id)
		if len(runs) == 0 || runs[0].ID == first.ID || runs[0].State == "pending" {
			return false
		}
		refused = runs[0]
		return true
	})
	setAutomationEnabled(t, cli, id, false)
	shown, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if tender := shown.Seed.Tender; tender == nil || protocol.Deref(tender.SessionID) != protocol.SessionID(other) {
		t.Fatalf("after %s took %s the singleton's run (%s, %q) left it with %+v", other, seed, refused.State, protocol.Deref(refused.LastError), shown.Seed.Tender)
	}
	if refused.State != "failed" || !strings.Contains(protocol.Deref(refused.LastError), "being tended by") {
		t.Fatalf("the run is %s with %q, want it failed naming the seed's tender", refused.State, protocol.Deref(refused.LastError))
	}
}

func TestASingletonWhoseNewSessionNeverStartedOpensItOnTheNextRun(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "*/2 * * * *", "@every 1s", 1)
	id := applyAutomation(t, cli, spec).ID
	awaitSettled := func(earlier ...string) (run protocol.AutomationRunSummary) {
		t.Helper()
		w.tickSchedulesUntil("a settled run", func() bool {
			runs := automationRuns(t, cli, id)
			if len(runs) == 0 || runs[0].State == "pending" || slices.Contains(earlier, runs[0].ID) {
				return false
			}
			run = runs[0]
			return true
		})
		setAutomationEnabled(t, cli, id, false)
		return run
	}
	first := awaitSettled()
	seed := protocol.Deref(first.SeedID)
	w.Launched(string(protocol.Deref(first.SessionID))).Prompted()
	applyAutomation(t, cli, automationEditSpec(id, strings.Replace(spec, "prompt: Sweep.", "prompt: Sweep the branches.", 1)))
	other := w.Spawn(app, fakeagent.Claude, w.Path(""))
	w.Launched(other)
	if _, err := cli.SeedTransition(protocol.SessionID(other), seed, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	setAutomationEnabled(t, cli, id, true)
	if refused := awaitSettled(first.ID); refused.State != "failed" {
		t.Fatalf("with %s holding %s the run is %s, want it refused", other, seed, refused.State)
	}
	gardenNudgeMove(t, cli, other, seed, "park")

	setAutomationEnabled(t, cli, id, true)
	var next protocol.AutomationRunSummary
	w.tickSchedulesUntil("a delivered run", func() bool {
		runs := automationRuns(t, cli, id)
		if len(runs) == 0 || runs[0].State != "delivered" {
			return false
		}
		next = runs[0]
		return true
	})
	setAutomationEnabled(t, cli, id, false)
	if protocol.Deref(next.SeedID) != seed {
		t.Fatalf("once %s let go, the run is on seed %s, want %s", other, protocol.Deref(next.SeedID), seed)
	}
	if opened := w.Launched(string(protocol.Deref(next.SessionID))); opened.Resumed || !strings.Contains(opened.Prompted(), "Sweep the branches.") {
		t.Fatalf("the never-started session came up as %+v, want it opened on the new prompt", opened)
	}
}
