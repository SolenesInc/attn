package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADelegationWhoseAgentCannotStartRemovesItsPaneAndKeepsItsWorktree(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	repo := newRepo(t, "shop")
	root := filepath.Dir(repo)
	source := w.Spawn(app, fakeagent.Codex, repo)
	w.Launched(source)
	before := w.App().Initial

	defer w.RefusePiLaunches("pi could not start: the model provider is unreachable")()
	for _, row := range []struct {
		name, source, branch string
	}{
		{name: "into the caller's workspace", source: source},
		{name: "into a new worktree beside the caller", source: source, branch: "feature/beside"},
		{name: "into a new worktree of its own", branch: "feature/alone"},
	} {
		request := brief(repo, "Add a discount field")
		request.Agent = protocol.Ptr("pi")
		request.Label = protocol.Ptr(strings.ReplaceAll(row.name, " ", "-"))
		if row.source != "" {
			request.SourceSessionID = protocol.Ptr(row.source)
		}
		request.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"}
		request.AllowWorktreeReuse = protocol.Ptr(true)
		if row.branch != "" {
			request.Checkout = delegateNewWorktree(row.branch, "main")
		}
		if result, err := cli.Delegate(request); err == nil || !strings.Contains(err.Error(), "provider is unreachable") {
			t.Errorf("%s: delegating = %+v, %v; want pi's launch failure", row.name, result, err)
		}
		if row.branch != "" {
			worktree := filepath.Join(root, "shop--"+strings.ReplaceAll(row.branch, "/", "-"))
			if _, err := os.Stat(worktree); err != nil {
				t.Errorf("%s: the failed delegation took away the worktree it created at %s: %v", row.name, worktree, err)
			}
		}
	}

	after := w.App().Initial
	if len(after.Sessions) != 1 || after.Sessions[0].ID != source {
		t.Errorf("after the failed delegations the sessions are %+v, want only the caller %s", after.Sessions, source)
	}
	if !slices.EqualFunc(after.Workspaces, before.Workspaces, func(a, b protocol.Workspace) bool {
		return a.ID == b.ID && slices.Equal(delegatePaneSessions(a), delegatePaneSessions(b))
	}) {
		t.Errorf("after the failed delegations the workspaces are %+v, want them as before: %+v", after.Workspaces, before.Workspaces)
	}
}

func TestADelegationFailsNamingTheScreenWhenItsAgentExitsBeforeItsFirstTurn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	const screen = `Error: Model "gpt-5.6-sol" is ambiguous across providers`
	for _, agent := range []fakeagent.Harness{fakeagent.Codex, fakeagent.Pi} {
		request := brief(cwd, "Say hello")
		request.Agent = protocol.Ptr(string(agent))
		request.Label = protocol.Ptr("hello-" + string(agent))
		w.ExitAtNextBoot(1, screen)
		_, err := cli.Delegate(request)
		for _, want := range []string{string(agent) + " exited with code 1 before its first turn", "attn agent peek", "is ambiguous across providers"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("delegating to %s = %v, want the failure to say %q", agent, err, want)
			}
		}

		var delegate protocol.Session
		for _, session := range w.App().Initial.Sessions {
			if session.Label == "hello-"+string(agent) {
				delegate = session
			}
		}
		if delegate.ID == "" {
			t.Fatalf("the %s delegate that exited was removed; it is the evidence", agent)
		}
		if exit, _ := peekExit(t, cli, delegate.ID, "is ambiguous across providers"); exit.Code != 1 {
			t.Errorf("peek of the %s delegate shows exit %+v, want code 1", agent, exit)
		}
		notes, err := cli.SeedNotes("", protocol.Deref(delegate.SeedID), 5)
		if err != nil || len(notes.Notes) == 0 || !strings.Contains(notes.Notes[0].Body, "exited with code 1 before starting its first turn") ||
			!strings.Contains(notes.Notes[0].Body, "is ambiguous across providers") {
			t.Errorf("the %s delegate's seed notes are %+v, %v; want the exit explained", agent, notes, err)
		}
	}
}
