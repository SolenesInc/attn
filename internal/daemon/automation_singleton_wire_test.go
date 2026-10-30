package daemon_test

import (
	"os"
	"regexp"
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
