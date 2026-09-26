package daemon_test

import (
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

func workspaceOfDelegate(t *testing.T, w *world, workspaceID string) protocol.Workspace {
	t.Helper()
	for _, workspace := range w.App().Initial.Workspaces {
		if workspace.ID == workspaceID {
			return workspace
		}
	}
	t.Fatalf("the app sees no workspace %s", workspaceID)
	return protocol.Workspace{}
}

func delegatePaneSessions(workspace protocol.Workspace) []string {
	var sessions []string
	if workspace.Layout != nil {
		for _, pane := range workspace.Layout.Panes {
			sessions = append(sessions, protocol.Deref(pane.SessionID))
		}
	}
	return sessions
}

func TestADelegateStartsOnASeedPointerInItsCallersWorkspace(t *testing.T) {
	for _, agent := range []fakeagent.Harness{fakeagent.Codex, fakeagent.Copilot} {
		t.Run(string(agent), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, agent)
			app, cli := w.App(), w.Client()
			cwd := w.Path("api")
			source := w.Spawn(app, fakeagent.Codex, cwd)
			sourceWorkspace := "workspace-api"
			if err := cli.ToggleWorkspaceMute(sourceWorkspace); err != nil {
				t.Fatal(err)
			}
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

			if protocol.Deref(result.WorkspaceID) != sourceWorkspace || result.Directory != cwd {
				t.Errorf("the delegate runs in workspace %q at %s; want the caller's %s at %s", protocol.Deref(result.WorkspaceID), result.Directory, sourceWorkspace, cwd)
			}
			workspace := workspaceOfDelegate(t, w, sourceWorkspace)
			if panes := delegatePaneSessions(workspace); len(panes) != 2 || panes[1] != result.SessionID {
				t.Errorf("the caller's workspace holds panes for %v; want the caller and then the delegate", panes)
			}
			if !workspace.Muted {
				t.Errorf("an ordinary delegation unmuted the caller's workspace %s", sourceWorkspace)
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

func TestADelegationFromTheChiefIsMarkedAndMadeVisible(t *testing.T) {
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
	if err := cli.ToggleWorkspaceMute("workspace-chief"); err != nil {
		t.Fatal(err)
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
	if workspace := workspaceOfDelegate(t, w, protocol.Deref(result.WorkspaceID)); workspace.ID != "workspace-chief" || workspace.Muted {
		t.Errorf("the chief's delegate landed in %+v; want the chief's own workspace, unmuted", workspace)
	}
}

func TestADelegationNamesItsWorkspaceSessionAndPane(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	longDirectory := strings.Repeat("invoice-", 7)
	for _, row := range []struct {
		name, directory, label, want string
	}{
		{name: "an explicit name", directory: "svc", label: "Payments API", want: "Payments API"},
		{name: "the directory's name", directory: "ledger", want: "ledger"},
		{name: "a long directory name cut to fit", directory: longDirectory, want: strings.TrimSuffix(strings.Repeat("invoice-", 6), "-")},
	} {
		cwd := w.Path(row.directory)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		request := brief(cwd, "Reconcile the ledgers")
		request.Agent = protocol.Ptr("codex")
		if row.label != "" {
			request.Label = protocol.Ptr(row.label)
		}
		result, err := cli.Delegate(request)
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		workspace := workspaceOfDelegate(t, w, protocol.Deref(result.WorkspaceID))
		if workspace.Title != row.want || workspace.Directory != cwd || workspace.Layout == nil || len(workspace.Layout.Panes) != 1 ||
			workspace.Layout.Panes[0].Title != row.want || protocol.Deref(workspace.Layout.Panes[0].SessionID) != result.SessionID {
			t.Errorf("%s: the new workspace is %+v; want %q at %s holding one pane titled %q for %s", row.name, workspace, row.want, cwd, row.want, result.SessionID)
		}
		if label := sessionOfDelegate(t, w, result.SessionID).Label; label != row.want {
			t.Errorf("%s: the delegate is labelled %q; want %q", row.name, label, row.want)
		}
	}
}

func TestADelegationThatCannotBePlacedIsRefusedBeforeAnythingLaunches(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	for _, name := range []string{"reviewer", "writer"} {
		w.Spawn(app, fakeagent.Codex, w.Path("docs"), func(m *protocol.SpawnSessionMessage) { m.ID, m.Label = name, protocol.Ptr(name) })
	}
	testworld.Request(app, protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-payments", Title: "Payments API", Directory: w.Path("docs"),
	}, protocol.EventWorkspaceRegistered, func(protocol.WebSocketEvent) bool { return true })
	takenDirectory := w.Path("elsewhere", "payments api")
	for _, dir := range []string{w.Path("svc"), takenDirectory} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	before := len(w.App().Initial.Workspaces)

	for _, row := range []struct {
		name, source, cwd, label, refusal string
	}{
		{name: "a name past 48 characters", cwd: w.Path("svc"), label: strings.Repeat("n", 49), refusal: "is too long (max 48 characters)"},
		{name: "a name that names nothing", cwd: w.Path("svc"), label: ".", refusal: `"." is not a usable name`},
		{name: "another workspace's name", cwd: w.Path("svc"), label: "payments api", refusal: `workspace name "payments api" is already in use`},
		{name: "a directory named like another workspace", cwd: takenDirectory, refusal: `workspace name "payments api" is already in use`},
		{name: "the caller's own name", source: "reviewer", cwd: w.Path("docs"), label: "Reviewer", refusal: `session name "Reviewer" is already used in this workspace`},
		{name: "a neighbour's name", source: "reviewer", cwd: w.Path("docs"), label: "WRITER", refusal: `session name "WRITER" is already used in this workspace`},
		{name: "a caller attn does not know", source: "missing-source", cwd: w.Path("svc"), refusal: "source session missing-source was not found"},
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

	if after := w.App().Initial; len(after.Workspaces) != before || len(after.Sessions) != 2 {
		t.Errorf("after only refused delegations the app sees %d workspaces (was %d) and sessions %+v", len(after.Workspaces), before, after.Sessions)
	}
}
