package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const shopRemote = "git@github.test:acme/shop.git"

func newRepoAt(t *testing.T, name, remote string) string {
	t.Helper()
	repo := newRepo(t, name)
	if remote != "" {
		runGit(t, repo, "remote", "add", "origin", remote)
	}
	return repo
}

func startConversationIn(w *world, repo string) {
	app := w.App()
	id := w.Spawn(app, fakeagent.Claude, repo)
	w.Launched(id)
	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return protocol.Deref(s.ConversationStarted) && protocol.Deref(s.RepositoryOrigin) != ""
	})
}

func claudeBrief(cwd, label string, checkout *protocol.DelegateCheckout) protocol.DelegateMessage {
	request := brief(cwd, "Say hello")
	request.Checkout = checkout
	request.Agent = protocol.Ptr("claude")
	request.Label = protocol.Ptr(label)
	return request
}

func reuseMain() *protocol.DelegateCheckout {
	return &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"}
}

func delegateAndAnswerTheQuestion(t *testing.T, w *world, cwd, label string, checkout *protocol.DelegateCheckout, whileAsked func()) {
	t.Helper()
	app, cli := w.App(), w.Client()
	w.AskFolderTrustAtNextBoot()
	request := claudeBrief(cwd, label, checkout)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		if _, err := cli.Delegate(request); err != nil {
			t.Errorf("delegating: %v", err)
		}
	}()
	session := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.Label == label
	}).Session
	app.AwaitScreen(string(session.ID), folderTrustChoice)
	whileAsked()
	app.TypeLine(string(session.ID), "\x1b[B")
	<-answered
}

func TestADelegationIntoANewWorktreeOfAKnownRepositoryRecordsTheMainRepositoryAndAsksNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	startConversationIn(w, newRepoAt(t, "shop-elsewhere", shopRemote))
	repo := newRepoAt(t, "shop", shopRemote)

	w.AskFolderTrustAtNextBoot()
	result, err := w.Client().Delegate(claudeBrief(repo, "known", delegateNewWorktree("nightly", "main")))
	if err != nil {
		t.Fatalf("delegating: %v", err)
	}
	if said := protocol.Deref(result.FirstTurnUnconfirmed); said != "" {
		t.Errorf("the first turn went unconfirmed: %s", said)
	}
	if !w.ClaudeFolderTrusted(repo) {
		t.Errorf("Claude's config does not trust the main repository %s", repo)
	}
	worktree := ""
	for _, s := range w.App().Initial.Sessions {
		if s.ID == result.SessionID {
			worktree = s.Directory
		}
	}
	if worktree == repo || w.ClaudeFolderTrusted(worktree) {
		t.Errorf("the delegation ran in %s and Claude's config trusts it, want a new worktree with the trust on the main repository alone", worktree)
	}
}

func TestADelegationIntoARepositoryWithNoKnownOriginLeavesTheQuestion(t *testing.T) {
	for _, c := range []struct{ name, remote string }{
		{"an origin no agent has worked in", shopRemote},
		{"no origin", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			repo := newRepoAt(t, "shop", c.remote)

			delegateAndAnswerTheQuestion(t, w, repo, "unknown", reuseMain(), func() {
				if w.ClaudeFolderTrusted(repo) {
					t.Errorf("Claude's config trusts %s, want it left to the user", repo)
				}
			})
		})
	}
}

func TestALaunchThatNeverPassedTheQuestionDoesNotMakeItsRepositoryKnown(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	repo := newRepoAt(t, "shop", shopRemote)
	app := w.App()
	w.AskFolderTrustAtNextBoot()
	stopped := w.Spawn(app, fakeagent.Claude, repo)
	app.AwaitScreen(stopped, folderTrustChoice)

	delegateAndAnswerTheQuestion(t, w, repo, "second", delegateNewWorktree("second", "main"), func() {
		if w.ClaudeFolderTrusted(repo) {
			t.Errorf("Claude's config trusts %s after a launch that never started a conversation", repo)
		}
	})
}

func manualClaudeAutomationIn(folder string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: claude}
location: {type: directory, path: %q}
`, folder)
}

func TestAClaudeAutomationRefusesARepositoryThatIsNeitherKnownNorTrusted(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	repo := newRepoAt(t, "shop", shopRemote)
	folder := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, manualClaudeAutomationIn(folder))
	awaitAutomationChanged(app, 1)

	answered := runAutomationFromApp(app, "refused")

	if answered.Success || !strings.Contains(protocol.Deref(answered.Error), folder) || !strings.Contains(protocol.Deref(answered.Error), "trust") {
		t.Fatalf("the run answered %s, want it refused naming %s and how to trust it", describeRunAnswer(answered), folder)
	}
	if answered.Run != nil && protocol.Deref(answered.Run.SessionID) != "" {
		t.Errorf("the refused run started session %s, want no terminal", protocol.Deref(answered.Run.SessionID))
	}
	if w.ClaudeFolderTrusted(repo) {
		t.Errorf("Claude's config trusts %s, want it left untrusted", repo)
	}
	requireTrustFix(t, app, "claude", repo)
}

func TestAClaudeAutomationRunsInAKnownOrAlreadyTrustedRepository(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(w *world, repo string)
	}{
		{"known", func(w *world, repo string) {
			startConversationIn(w, newRepoAt(t, "shop-elsewhere", shopRemote))
		}},
		{"already trusted", func(w *world, repo string) { w.SetClaudeFolderTrust(repo) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			repo := newRepoAt(t, "shop", shopRemote)
			c.setup(w, repo)
			applyAutomation(t, cli, manualClaudeAutomationIn(repo))
			awaitAutomationChanged(app, 1)

			answered := runAutomationFromApp(app, "runs")

			if !answered.Success || answered.Run == nil || protocol.Deref(answered.Run.SessionID) == "" {
				t.Fatalf("the run answered %s, want it started", describeRunAnswer(answered))
			}
			if !w.ClaudeFolderTrusted(repo) {
				t.Errorf("Claude's config does not trust %s", repo)
			}
			if got := w.Launched(string(protocol.Deref(answered.Run.SessionID))).Prompted(); !strings.Contains(got, "Report the flaky tests.") {
				t.Errorf("claude received %q, want the automation's prompt", got)
			}
		})
	}
}
