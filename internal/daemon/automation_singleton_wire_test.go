package daemon_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSingletonContractEditsSupersedeSessionsWithoutStoppingThem(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL", "1s")
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
	awaitRun := func(earlier string) protocol.AutomationRunSummary {
		t.Helper()
		for {
			awaitAutomationChanged(app, id)
			runs := automationRuns(t, cli, id)
			if len(runs) > 0 && runs[0].ID != earlier && runs[0].State == "delivered" {
				return runs[0]
			}
		}
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
	for {
		awaitAutomationChanged(app, id)
		runs := automationRuns(t, cli, id)
		if len(runs) == 0 || runs[0].State != "delivered" || protocol.Deref(runs[0].SessionID) == session {
			continue
		}
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
		listing, err := cli.List("")
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
		break
	}
}

func TestAnUnresumableSingletonStartsFreshAndAClosedOneResumesItsConversation(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL", "100ms")
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	id := applyAutomation(t, cli, strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "*/2 * * * *", "@every 1s", 1)).ID
	awaitDelivered := func(earlier ...string) protocol.AutomationRunSummary {
		t.Helper()
		for {
			if runs := automationRuns(t, cli, id); len(runs) > 0 && runs[0].State == "delivered" && !slices.Contains(earlier, runs[0].ID) {
				setAutomationEnabled(t, cli, id, false)
				return runs[0]
			}
			awaitAutomationChanged(app, id)
		}
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
