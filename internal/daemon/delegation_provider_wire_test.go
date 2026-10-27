package daemon_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegatedWorktreeProviderCanChooseItsOwnDirectory(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	repo := newRepo(t, "shop")
	provider := connectPlugin(t, w, "delegated-path-provider", "worktree.create")
	request := delegateCheckoutAt(repo, delegateNewWorktree("feat/provider", "main"))
	request.Agent = protocol.Ptr("claude")
	app.Send(request)
	var asked pluginWorktreeCreate
	id := provider.expect("worktree.create", &asked)
	if asked.RequestedPath != nil {
		t.Fatalf("provider received requested_path=%q; caller omitted it", *asked.RequestedPath)
	}
	path := filepath.Join(filepath.Dir(repo), "provider-chosen")
	runGit(t, repo, "worktree", "add", "-q", "-b", asked.Branch, path, asked.StartingFrom)
	provider.answer(id, pluginWorktreeAnswer{Status: "handled", Path: path, Branch: asked.Branch})
	result := testworld.Await(app, protocol.EventDelegateResult, func(protocol.DelegateResultMessage) bool { return true })
	if !result.Success || result.Result == nil || result.Result.Directory != path {
		t.Fatalf("delegation=%+v, error=%s; want provider's %s", result.Result, protocol.Deref(result.Error), path)
	}
	w.Launched(string(result.Result.SessionID)).Prompted()
}
