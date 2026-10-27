package main_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSessionTreeShowsDelegationChainsAndKeepsChildrenOfClosedDispatchers(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	if empty := s.Attn("session", "list", "--tree"); empty.Code != 0 || !strings.Contains(empty.Stdout, "no live sessions") {
		t.Fatalf("empty tree exited %d: %s%s", empty.Code, empty.Stdout, empty.Stderr)
	}
	app := s.App()
	cwd := s.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	source := s.Spawn(app, fakeagent.Claude, cwd, labelled("orchestrator"))
	sourceRun := s.Launched(source)
	settle(app, source)
	delegate := func(dispatcher, label, brief string) delegated {
		t.Helper()
		planted := s.Run(testworld.Invocation{Session: dispatcher, Args: []string{"seed", "plant", brief, "-m", brief, "--json"}})
		if planted.Code != 0 {
			t.Fatalf("planting %s exited %d: %s", label, planted.Code, planted.Stderr)
		}
		var seed protocol.Seed
		planted.JSON(t, &seed)
		started := s.Run(testworld.Invocation{Session: dispatcher, Args: []string{
			"delegate", "--seed", seed.ID, "--cwd", cwd, "--name", label, "--model", "default",
		}})
		if started.Code != 0 {
			t.Fatalf("delegating %s exited %d: %s", label, started.Code, started.Stderr)
		}
		var result delegated
		started.JSON(t, &result)
		run := s.Launched(result.SessionID)
		run.Prompted()
		run.Reply("Please review. <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, result.SessionID, func(x protocol.Session) bool {
			return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
		})
		return result
	}
	child := delegate(source, "builder", "Build the discount field")
	grandchild := delegate(child.SessionID, "reviewer", "Review the discount field")
	sibling := delegate(source, "writer", "Document the discount field")
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: protocol.SessionID(child.SessionID), Until: until})
	testworld.AwaitSession(app, child.SessionID, func(x protocol.Session) bool {
		return protocol.Deref(x.TurnSnoozedUntil) == until
	})
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), cwd, func(m *protocol.SpawnSessionMessage) {
		m.SpawnedFrom = protocol.Ptr(protocol.SessionID(source))
		m.Label = protocol.Ptr("shell")
	})
	testworld.AwaitSession(app, shell, func(x protocol.Session) bool {
		return protocol.Deref(x.ParentSessionID) == protocol.SessionID(source)
	})
	tree := func(args ...string) string {
		t.Helper()
		got := s.Attn(append([]string{"session", "list", "--tree"}, args...)...)
		if got.Code != 0 {
			t.Fatalf("session tree exited %d: %s", got.Code, got.Stderr)
		}
		return got.Stdout
	}
	shown := tree()
	for _, prefix := range []string{
		source[:8] + "  orchestrator",
		"├─ " + child.SessionID[:8] + "  builder",
		"│  └─ " + grandchild.SessionID[:8] + "  reviewer",
		"└─ " + sibling.SessionID[:8] + "  writer",
		shell[:8] + "  shell (shell split from " + source[:8] + ")",
	} {
		if !strings.Contains(shown, "\n"+prefix) {
			t.Errorf("tree is missing row %q:\n%s", prefix, shown)
		}
	}
	for _, tc := range []struct {
		id, label, turn, slug string
	}{
		{source, "orchestrator", "-", "-"},
		{child.SessionID, "builder", "snoozed until " + until, "build-discount-field"},
		{grandchild.SessionID, "reviewer", "owed", "review-discount-field"},
		{sibling.SessionID, "writer", "owed", "document-discount-field"},
	} {
		for _, line := range strings.Split(shown, "\n") {
			if strings.Contains(line, tc.id[:8]+"  "+tc.label) {
				if strings.Contains(line, tc.id) || !strings.Contains(line, "claude") || !strings.Contains(line, tc.turn) || !strings.HasSuffix(line, tc.slug) {
					t.Errorf("row %s is missing its short id, harness, turn or slug: %s", tc.label, line)
				}
				if tc.id != source && !strings.Contains(line, "waiting_input") {
					t.Errorf("row %s is missing its state: %s", tc.label, line)
				}
			}
		}
	}
	if got := tree("--profile", app.SelectedProfile()); got != shown {
		t.Errorf("profile tree differs:\n%s\nall:\n%s", got, shown)
	}
	if got := tree("--profile", "other-profile"); !strings.Contains(got, "no sessions match those filters") {
		t.Errorf("other profile tree printed %q", got)
	}
	for _, args := range [][]string{{"--closed"}, {"--all"}, {"--limit", "1"}, {"--before", source}, {"--repository", cwd}, {"--last", "today"}, {"--since", "2026-01-01"}, {"--until", "2026-12-31"}, {"--reopen"}, {"--json"}} {
		refused := s.Attn(append([]string{"session", "list", "--tree"}, args...)...)
		if refused.Code != 2 || refused.Stdout != "" || !strings.Contains(refused.Stderr, "--tree supports only --profile") {
			t.Errorf("tree with %q exited %d: %s%s", args, refused.Code, refused.Stdout, refused.Stderr)
		}
	}
	sourceRun.Exit(143)
	testworld.AwaitSession(app, source, func(x protocol.Session) bool {
		return protocol.Deref(x.StateReason) == "process_exited"
	})
	if stopped := tree(); !strings.Contains(stopped, "\n├─ "+child.SessionID[:8]+"  builder") || !strings.Contains(stopped, source[:8]+"  orchestrator") {
		t.Fatalf("stopping the dispatcher detached its live delegates:\n%s", stopped)
	}
	closed := s.Run(testworld.Invocation{Session: source, Args: []string{"agent", "close", source, "-m", "Delegation underway"}})
	if closed.Code != 0 {
		t.Fatalf("close dispatcher exited %d: %s", closed.Code, closed.Stderr)
	}
	shown = tree()
	for _, row := range []string{
		child.SessionID[:8] + "  builder (dispatcher closed or absent)",
		"└─ " + grandchild.SessionID[:8] + "  reviewer",
		sibling.SessionID[:8] + "  writer (dispatcher closed or absent)",
	} {
		if !strings.Contains(shown, "\n"+row) {
			t.Errorf("tree after closing dispatcher is missing %q:\n%s", row, shown)
		}
	}
	if strings.Contains(shown, source[:8]+"  orchestrator") {
		t.Errorf("tree still lists the closed dispatcher:\n%s", shown)
	}
	app.TypeLine(shell, "exit")
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == shell })
}

func TestSessionTreeKeepsAgentReadsInTheirProfileWithoutConsumingSeedNotifications(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	homeProfile := app.SelectedProfile()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	sideProfile := created.Profile.ID
	sideApp := s.AppOn(sideProfile)
	cwd := s.Path("profiles")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	delegate := func(peer *testworld.Peer, name string) (delegated, *fakeagent.Run) {
		t.Helper()
		source := s.Spawn(peer, fakeagent.Claude, cwd, labelled(name+" planner"))
		s.Launched(source)
		started := s.Run(testworld.Invocation{Session: source, Args: []string{
			"delegate", "--brief", "Work in this profile", "--cwd", cwd, "--name", name + " delegate", "--model", "default",
		}})
		if started.Code != 0 {
			t.Fatalf("delegate %s: %s", name, started.Stderr)
		}
		var result delegated
		started.JSON(t, &result)
		run := s.Launched(result.SessionID)
		run.Prompted()
		run.Reply("Ready. <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(peer, result.SessionID, func(x protocol.Session) bool {
			return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
		})
		return result, run
	}
	home, homeAgent := delegate(app, "home")
	side, _ := delegate(sideApp, "side")
	tree := func(caller string, args ...string) string {
		t.Helper()
		result := s.Run(testworld.Invocation{Session: caller, Args: append([]string{"session", "list", "--tree"}, args...)})
		if result.Code != 0 {
			t.Fatalf("tree for %s with %q: %s", caller, args, result.Stderr)
		}
		return result.Stdout
	}
	user := tree("")
	for _, slug := range []string{"home-delegate", "side-delegate"} {
		if !strings.Contains(user, slug) {
			t.Errorf("user tree is missing %s:\n%s", slug, user)
		}
	}
	for _, tc := range []struct {
		caller, profile, own, foreign string
	}{
		{"", homeProfile, "home-delegate", "side-delegate"},
		{"", sideProfile, "side-delegate", "home-delegate"},
		{home.SessionID, "", "home-delegate", "side-delegate"},
		{side.SessionID, "", "side-delegate", "home-delegate"},
		{home.SessionID, homeProfile, "home-delegate", "side-delegate"},
	} {
		var args []string
		if tc.profile != "" {
			args = []string{"--profile", tc.profile}
		}
		shown := tree(tc.caller, args...)
		if !strings.Contains(shown, tc.own) || strings.Contains(shown, tc.foreign) {
			t.Errorf("tree for %s with %q crosses profile scope:\n%s", tc.caller, args, shown)
		}
	}
	foreign := tree(home.SessionID, "--profile", sideProfile)
	missing := tree(home.SessionID, "--profile", "missing-profile")
	if foreign != missing || !strings.Contains(foreign, "no sessions match those filters") {
		t.Errorf("foreign profile %q and missing profile %q should both be empty", foreign, missing)
	}
	note := s.Attn("seed", "note", home.SeedID, "--profile", homeProfile, "-m", "Review the result", "--ring")
	if note.Code != 0 {
		t.Fatalf("ring home delegate: %s", note.Stderr)
	}
	homeAgent.Prompted()
	if shown := tree(home.SessionID); !strings.Contains(shown, "home-delegate") || strings.Contains(shown, "side-delegate") {
		t.Errorf("notified agent's tree crosses profile scope:\n%s", shown)
	}
	var inbox protocol.AgentInboxBatchResult
	s.Run(testworld.Invocation{Session: home.SessionID, Args: []string{"agent", "inbox", "--json"}}).JSON(t, &inbox)
	for _, item := range inbox.Items {
		if strings.Contains(item.Content, home.SeedID) {
			return
		}
	}
	t.Fatalf("tree consumed the pending seed notification: %+v", inbox.Items)
}

func TestSessionTreeFollowsMemberDispatcherAcrossClearSleepAndWake(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	var member protocol.CrewCreateResult
	s.Attn("crew", "create", "Keel", "--agent", "claude", "--json").JSON(t, &member)
	var wake protocol.CrewWakeResult
	s.Attn("crew", "wake", "Keel", "--json").JSON(t, &wake)
	day := s.Launched(string(wake.SessionID))
	day.Prompted()
	day.Reply("Ready. <!-- attn:state=idle -->")
	var child delegated
	s.Run(testworld.Invocation{Session: string(wake.SessionID), Args: []string{
		"delegate", "--brief", "Build the member's work", "--cwd", member.Member.HomeDir,
		"--name", "builder", "--model", "default",
	}}).JSON(t, &child)
	s.Launched(child.SessionID).Prompted()
	assertTree := func(parent protocol.SessionID, absent bool) {
		t.Helper()
		result := s.Attn("session", "list", "--tree")
		if result.Code != 0 {
			t.Fatalf("tree: %+v", result)
		}
		row := "└─ " + child.SessionID[:8] + "  builder"
		if absent {
			row = child.SessionID[:8] + "  builder (dispatcher Keel closed or absent)"
		} else if !strings.Contains(result.Stdout, string(parent[:8])+"  Keel") {
			t.Fatalf("member's current session absent from tree:\n%s", result.Stdout)
		}
		if !strings.Contains(result.Stdout, "\n"+row) {
			t.Fatalf("member delegate absent from tree:\n%s", result.Stdout)
		}
	}
	assertTree(wake.SessionID, false)
	app.TypeLine(string(wake.SessionID), "/clear")
	if got := day.Prompted(); got != "/clear" {
		t.Fatalf("clear = %q", got)
	}
	next := *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == wake.SessionID
	}).Session
	testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		for _, m := range e.Members {
			if m.Key == member.Member.Key && protocol.Deref(m.BindingSession) == next.ID {
				return true
			}
		}
		return false
	})
	assertTree(next.ID, false)
	if _, err := s.Client().CrewHandoff(next.ID, "The work is delegated.", false, protocol.CrewDayCloseSleep, ""); err != nil {
		t.Fatal(err)
	}
	assertTree("", true)
	s.Attn("crew", "wake", "Keel", "--json").JSON(t, &wake)
	s.Launched(string(wake.SessionID)).Prompted()
	assertTree(wake.SessionID, false)
}
