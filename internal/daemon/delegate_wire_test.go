package daemon_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func delegateFrom(source, cwd, text string, agent fakeagent.Harness) protocol.DelegateMessage {
	request := brief(cwd, text)
	request.SourceSessionID = protocol.Ptr(source)
	request.Agent = protocol.Ptr(string(agent))
	return request
}

func desktopOfDelegate(t *testing.T, w *world, desktopID string) protocol.Desktop {
	t.Helper()
	for _, desktop := range w.App().Initial.Desktops {
		if desktop.ID == desktopID {
			return desktop
		}
	}
	t.Fatalf("the app sees no desktop %s", desktopID)
	return protocol.Desktop{}
}

func delegatePaneSessions(desktop protocol.Desktop) []string {
	var sessions []string
	for _, pane := range desktop.Panes {
		sessions = append(sessions, pane.SessionID)
	}
	return sessions
}

func TestADelegateStartsOnASeedPointerBesideItsCaller(t *testing.T) {
	for _, agent := range []fakeagent.Harness{fakeagent.Codex, fakeagent.Copilot} {
		t.Run(string(agent), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, agent)
			app, cli := w.App(), w.Client()
			cwd := w.Path("api")
			sourceResult, sourceDesktop, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
			source := sourceResult.ID
			request := delegateFrom(source, cwd, "Migrate the store to the new schema", agent)
			request.RequestID = "migrate"
			request.Label = protocol.Ptr("Store migration")

			reply := testworld.Request(app, request, protocol.EventDelegateResult,
				func(m protocol.DelegateResultMessage) bool { return protocol.Deref(m.RequestID) == "migrate" })
			if !reply.Success {
				t.Fatalf("delegating to %s: %s", agent, protocol.Deref(reply.Error))
			}
			result := reply.Result
			if result.FirstTurnAt == nil {
				t.Errorf("the delegation returned %+v; want the first turn it saw", result)
			}
			prompt := w.Launched(result.SessionID).Prompted()
			if !strings.Contains(prompt, "attn seed show "+result.SeedID) {
				t.Errorf("the delegate was prompted %q; want a pointer to seed %s", prompt, result.SeedID)
			}
			for _, copied := range []string{"Migrate the store", "attn seed note", "attn seed harvest", "attn seed attach", "attn seed detach", "attn seed link", "attn seed wither", "attn ticket"} {
				if strings.Contains(prompt, copied) {
					t.Errorf("the delegate's prompt carries %q: %q", copied, prompt)
				}
			}

			shown, err := cli.SeedShow("", result.SeedID)
			if err != nil {
				t.Fatal(err)
			}
			seed := shown.Seed
			if seed.Body != "Migrate the store to the new schema" || seed.Title != "Store migration" || seed.Status != "growing" ||
				seed.PlanterSession != source || seed.TenderSession != result.SessionID {
				t.Errorf("the delegation planted %+v; want the brief, growing, planted by %s and tended by %s", seed, source, result.SessionID)
			}

			if protocol.Deref(result.DesktopID) != sourceDesktop || result.Directory != cwd {
				t.Errorf("the delegate runs on desktop %q at %s; want the caller's %s at %s", protocol.Deref(result.DesktopID), result.Directory, sourceDesktop, cwd)
			}
			if panes := delegatePaneSessions(desktopOfDelegate(t, w, sourceDesktop)); len(panes) != 2 || panes[1] != result.SessionID {
				t.Errorf("the caller's desktop holds panes for %v; want the caller and then the delegate", panes)
			}
			delegate := testworld.AwaitSession(app, result.SessionID, func(s protocol.Session) bool { return protocol.Deref(s.SeedID) != "" })
			if protocol.Deref(delegate.SeedID) != result.SeedID || string(delegate.Agent) != string(agent) || protocol.Deref(delegate.DelegatedFromChief) {
				t.Errorf("the app sees the delegate as %+v; want a %s session on seed %s, not delegated from a chief", delegate, agent, result.SeedID)
			}
		})
	}
}

func delegateLaunchFlag(argv []string, name string) (string, bool) {
	for i, arg := range argv {
		if arg == name && i+1 < len(argv) {
			return argv[i+1], true
		}
		if value, found := strings.CutPrefix(arg, name+"="); found {
			return value, true
		}
	}
	return "", false
}

func TestADelegatesModelAndEffortReachItsCommandLineOrAreRefusedByFlag(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Copilot, fakeagent.Pi)
	cli := w.Client()
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name, agent, model, effort string
		wantModel, wantEffort      string
		refusal                    string
	}{
		{name: "claude pinned", agent: "claude", model: "claude-fable-5", effort: "Low", wantModel: "claude-fable-5", wantEffort: "low"},
		{name: "claude at the default effort", agent: "claude", model: "opus", wantModel: "opus", wantEffort: "medium"},
		{name: "copilot unpinned", agent: "copilot"},
		{name: "pi unpinned", agent: "pi"},
		{name: "copilot with a model", agent: "copilot", model: "gpt-5", refusal: `agent "copilot" does not support --model`},
		{name: "copilot with an effort", agent: "copilot", effort: "high", refusal: `agent "copilot" does not support --effort`},
		{name: "pi with a model", agent: "pi", model: "glm-5", refusal: `agent "pi" does not support --model`},
	} {
		t.Run(row.name, func(t *testing.T) {
			request := brief(cwd, "Tighten the retry loop")
			request.Agent = protocol.Ptr(row.agent)
			request.Label = protocol.Ptr(strings.ReplaceAll(row.name, " ", "-"))
			if row.model != "" {
				request.Model = protocol.Ptr(row.model)
			}
			if row.effort != "" {
				request.Effort = protocol.Ptr(row.effort)
			}
			result, err := cli.Delegate(request)
			if row.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), row.refusal) {
					t.Fatalf("delegating = %+v, %v; want the refusal %q", result, err, row.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			argv := w.Launched(result.SessionID).Argv
			if model, _ := delegateLaunchFlag(argv, "--model"); model != row.wantModel {
				t.Errorf("%s launched with --model %q; want %q (argv %q)", row.agent, model, row.wantModel, argv)
			}
			if effort, _ := delegateLaunchFlag(argv, "--effort"); effort != row.wantEffort {
				t.Errorf("%s launched with --effort %q; want %q (argv %q)", row.agent, effort, row.wantEffort, argv)
			}
			if !strings.Contains(strings.Join(argv, " "), "attn seed show "+result.SeedID) {
				t.Errorf("%s launched without its seed pointer: %q", row.agent, argv)
			}
		})
	}
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 4 {
		t.Errorf("%d sessions exist; want only the four delegates that were not refused", len(sessions))
	}
}

func sessionOfDelegate(t *testing.T, w *world, sessionID string) protocol.Session {
	t.Helper()
	for _, session := range w.App().Initial.Sessions {
		if session.ID == sessionID {
			return session
		}
	}
	t.Fatalf("the app sees no session %s", sessionID)
	return protocol.Session{}
}

func TestADelegationFromTheChiefIsMarkedAndLandsBesideTheChief(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "chief")
	cwd := w.Path("chief")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if result := setChiefOfStaff(app, "chief", true); !result.Success {
		t.Fatalf("making chief the chief: %s", protocol.Deref(result.Error))
	}

	request := delegateFrom("chief", cwd, "Audit the backlog", fakeagent.Codex)
	request.Label = protocol.Ptr("Backlog audit")
	result, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("delegating from the chief: %v", err)
	}
	if prompt := w.Launched(result.SessionID).Prompted(); !strings.Contains(prompt, "attn seed show "+result.SeedID) {
		t.Errorf("the chief's delegate was prompted %q; want a pointer to seed %s", prompt, result.SeedID)
	}
	if shown, err := cli.SeedShow("", result.SeedID); err != nil || shown.Seed.TenderSession != result.SessionID || shown.Seed.Body != "Audit the backlog" {
		t.Errorf("the chief's delegation planted %+v, %v; want the brief tended by %s", shown, err, result.SessionID)
	}
	delegate := testworld.AwaitSession(app, result.SessionID, func(s protocol.Session) bool { return protocol.Deref(s.DelegatedFromChief) })
	if protocol.Deref(delegate.ChiefOfStaff) {
		t.Errorf("the chief's delegate reads as the chief itself: %+v", delegate)
	}
	if chief := sessionOfDelegate(t, w, "chief"); !protocol.Deref(chief.ChiefOfStaff) || protocol.Deref(chief.DelegatedFromChief) {
		t.Errorf("the chief reads as %+v; want chief_of_staff and not delegated_from_chief", chief)
	}
	chiefDesktop, _ := placedPane(t, w, "chief")
	if protocol.Deref(result.DesktopID) != chiefDesktop.ID {
		t.Errorf("the chief's delegate landed on desktop %q; want the chief's own %s", protocol.Deref(result.DesktopID), chiefDesktop.ID)
	}
}

func TestADelegationNamesItsSeedAndSessionFromItsBrief(t *testing.T) {
	longTitle := strings.Repeat("invoice ", 10) + "reconciliation more detail"
	for i, row := range []struct{ name, brief, label, title, want string }{
		{name: "heading", brief: "# Fix the queue jump\n\nInvestigate queue movement.", title: "Fix the queue jump", want: "Fix the queue jump"},
		{name: "hash in the heading text", brief: "# Document C#", title: "Document C#", want: "Document C#"},
		{name: "closing hashes after a text hash", brief: "# Document C# ###", title: "Document C#", want: "Document C#"},
		{name: "later heading", brief: "Context first\n\n### Reconcile  the\tledgers ###\nBody", title: "Reconcile the ledgers", want: "Reconcile the ledgers"},
		{name: "first non-empty line", brief: "\n\t Reconcile  the ledgers \nDetails", title: "Reconcile the ledgers", want: "Reconcile the ledgers"},
		{name: "long first line", brief: longTitle, title: strings.TrimSpace(strings.Repeat("invoice ", 10)), want: "invoice invoice invoice invoice invoice invoice"},
		{name: "unicode", brief: "###### " + strings.Repeat("é", 90), title: strings.Repeat("é", 80), want: strings.Repeat("é", 48)},
		{name: "explicit name", brief: "# Reconcile the ledgers", label: "Payments API", title: "Payments API", want: "Payments API"},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			cli := w.Client()
			cwd := w.Path(string(rune('a' + i)))
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			request := brief(cwd, row.brief)
			request.Agent = protocol.Ptr("codex")
			if row.label != "" {
				request.Label = protocol.Ptr(row.label)
			}
			result, err := cli.Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			shown, err := cli.SeedShow("", result.SeedID)
			if err != nil {
				t.Fatal(err)
			}
			if shown.Seed.Title != row.title || shown.Seed.Body != strings.TrimSpace(row.brief) {
				t.Errorf("seed = %+v; want title %q and original brief", shown.Seed, row.title)
			}
			session := sessionOfDelegate(t, w, result.SessionID)
			if session.Label != row.want {
				t.Errorf("label = %q; want %q", session.Label, row.want)
			}
			if prompt := w.Launched(result.SessionID).Prompted(); !strings.Contains(prompt, row.title) {
				t.Errorf("opening = %q; want seed title %q", prompt, row.title)
			}
		})
	}
}

func TestDerivedDelegationNamesUseTheFirstFreeDesktopSuffix(t *testing.T) {
	for _, mode := range []string{"beside a caller", "standalone", "unplaced caller"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			var sourceID, desktop string
			if mode == "beside a caller" {
				source, sourceDesktop, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("source"))
				sourceID, desktop = source.ID, sourceDesktop
			} else if mode == "unplaced caller" {
				sourceID = "caller"
				registerSessions(t, w, cli, sourceID)
			}
			cwd := w.Path("task")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, title := range []string{"Fix the queue jump", "Reconcile v1.2.", strings.Repeat("é", 70)} {
				for n := 1; n <= 3; n++ {
					request := brief(cwd, "# "+title+"\n\nDo the task.")
					request.Agent = protocol.Ptr("codex")
					if sourceID != "" {
						request.SourceSessionID = protocol.Ptr(sourceID)
					}
					result, err := cli.Delegate(request)
					if err != nil {
						t.Fatal(err)
					}
					want := title
					if n == 1 {
						want = string([]rune(title)[:min(48, len([]rune(title)))])
					} else {
						want = string([]rune(title)[:min(44, len([]rune(title)))]) + fmt.Sprintf(" (%d)", n)
					}
					if session := sessionOfDelegate(t, w, result.SessionID); session.Label != want {
						t.Errorf("label = %q; want %q", session.Label, want)
					}
					if desktop == "" {
						desktop = protocol.Deref(result.DesktopID)
					}
					if protocol.Deref(result.DesktopID) != desktop {
						t.Errorf("desktop = %s; want %s", protocol.Deref(result.DesktopID), desktop)
					}
					if shown, err := cli.SeedShow("", result.SeedID); err != nil || shown.Seed.Title != title {
						t.Errorf("seed = %+v, %v; want title %q", shown, err, title)
					}
				}
			}
		})
	}
}

func TestAnExistingSeedNamesItsDelegateAndHandover(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	source := w.Spawn(app, fakeagent.Codex, w.Path("source"))
	title := "Fix the queue jump"
	seed := plantDelegationSeed(t, cli, source, title)
	cwd := w.Path("task")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := cli.Delegate(delegateAtSeed(source, cwd, seed))
	if err != nil {
		t.Fatal(err)
	}
	if label := sessionOfDelegate(t, w, first.SessionID).Label; label != title {
		t.Errorf("seed delegate label = %q; want %q", label, title)
	}
	request := delegateAtSeed(source, cwd, seed)
	request.Assignment.Handover = &protocol.DelegateHandover{}
	second, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	if label := sessionOfDelegate(t, w, second.SessionID).Label; label != title+" (2)" {
		t.Errorf("handover label = %q; want suffix (2)", label)
	}
	if prompt := w.Launched(second.SessionID).Prompted(); !strings.Contains(prompt, title) {
		t.Errorf("handover opening = %q; want title", prompt)
	}
	request = delegateAtSeed(source, cwd, seed)
	request.Assignment.Handover = &protocol.DelegateHandover{}
	request.Label = protocol.Ptr("Queue reviewer")
	third, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	if label := sessionOfDelegate(t, w, third.SessionID).Label; label != "Queue reviewer" {
		t.Errorf("explicit handover label = %q", label)
	}
	if shown, err := cli.SeedShow("", seed); err != nil || shown.Seed.Title != title {
		t.Errorf("seed = %+v, %v; want title unchanged", shown, err)
	}
}

func TestADelegationThatCannotBePlacedIsRefusedBeforeAnythingLaunches(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	for _, name := range []string{"reviewer", "writer"} {
		w.Spawn(app, fakeagent.Codex, w.Path("docs"), func(m *protocol.SpawnSessionMessage) { m.ID, m.Label = name, protocol.Ptr(name) })
	}
	if err := os.MkdirAll(w.Path("svc"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		name, source, cwd, label, refusal string
	}{
		{name: "a name past 48 characters", cwd: w.Path("svc"), label: strings.Repeat("n", 49), refusal: "is too long (max 48 characters)"},
		{name: "a name that names nothing", cwd: w.Path("svc"), label: ".", refusal: `"." is not a usable name`},
		{name: "the caller's own name", source: "reviewer", cwd: w.Path("docs"), label: "Reviewer", refusal: `session name "Reviewer" is already used on this desktop`},
		{name: "a neighbour's name", source: "reviewer", cwd: w.Path("docs"), label: "WRITER", refusal: `session name "WRITER" is already used on this desktop`},
		{name: "a caller attn does not know", source: "missing-source", cwd: w.Path("svc"), refusal: "session missing-source"},
	} {
		request := brief(row.cwd, "Reconcile the ledgers")
		request.Agent = protocol.Ptr("codex")
		if row.source != "" {
			request.SourceSessionID = protocol.Ptr(row.source)
		}
		if row.label != "" {
			request.Label = protocol.Ptr(row.label)
		}
		if result, err := cli.Delegate(request); err == nil || !strings.Contains(err.Error(), row.refusal) {
			t.Errorf("%s: delegating = %+v, %v; want the refusal %q", row.name, result, err, row.refusal)
		}
	}

	if after := w.App().Initial; len(after.Sessions) != 2 {
		t.Errorf("after only refused delegations the app sees sessions %+v, want only the two it started", after.Sessions)
	}
}

func TestADelegateAtACustomDirectoryStaysBesideItsCaller(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	source, desktop, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("source"))
	target := w.Path("target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	request := delegateFrom(source.ID, target, "Work in the target directory", fakeagent.Codex)
	result, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Directory != target || protocol.Deref(result.DesktopID) != desktop {
		t.Fatalf("delegation = %+v; want %s beside the source on %s", result, target, desktop)
	}
	if session := sessionOfDelegate(t, w, result.SessionID); session.Directory != target {
		t.Errorf("session directory = %s; want %s", session.Directory, target)
	}
}
