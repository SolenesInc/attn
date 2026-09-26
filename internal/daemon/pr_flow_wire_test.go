package daemon_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestVisitingAPullRequestClearsItsNewChangesUntilItMovesAgain(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	pr := protocol.PR{
		ID: "github.com:acme/shop#1", Repo: "acme/shop", Host: "github.com", Number: 1, Title: "Add checkout",
		Author: "octocat", Role: protocol.PRRoleReviewer, State: protocol.PRStateWaiting,
		URL: "https://github.com/acme/shop/pull/1", HeadSHA: protocol.Ptr("a1"),
	}
	prFlowInject(t, w, pr)
	prFlowAwait(app, pr.ID, func(shown protocol.PR) bool { return !shown.HasNewChanges })

	app.Send(protocol.PRVisitedMessage{Cmd: protocol.CmdPRVisited, ID: pr.ID})
	prFlowAwait(app, pr.ID, func(shown protocol.PR) bool { return protocol.Deref(shown.HeatState) == protocol.HeatStateHot })

	pr.HeadSHA = protocol.Ptr("b2")
	prFlowInject(t, w, pr)
	prFlowAwait(app, pr.ID, func(shown protocol.PR) bool { return shown.HasNewChanges && protocol.Deref(shown.HeadSHA) == "b2" })

	app.Send(protocol.PRVisitedMessage{Cmd: protocol.CmdPRVisited, ID: pr.ID})
	prFlowAwait(app, pr.ID, func(shown protocol.PR) bool { return !shown.HasNewChanges })

	app.Send(protocol.MutePRMessage{Cmd: protocol.CmdMutePR, ID: pr.ID})
	prFlowAwait(app, pr.ID, func(shown protocol.PR) bool { return shown.Muted })
	app.Send(protocol.MuteRepoMessage{Cmd: protocol.CmdMuteRepo, Repo: pr.Repo})
	testworld.Await(app, protocol.EventReposUpdated, func(e protocol.WebSocketEvent) bool { return repoStates(e.Repos)[pr.Repo].Muted })

	w.restart()
	prs, err := w.Client().QueryPRs("")
	if err != nil {
		t.Fatal(err)
	}
	if kept := prNumbered(t, prs, 1); kept.HasNewChanges || !kept.Muted {
		t.Errorf("after a restart the pull request shows new changes %v and muted %v, want it visited and muted", kept.HasNewChanges, kept.Muted)
	}
}

func prFlowInject(t *testing.T, w *world, pr protocol.PR) {
	t.Helper()
	payload, err := json.Marshal(protocol.InjectTestPRMessage{Cmd: protocol.CmdInjectTestPR, PR: pr})
	if err != nil {
		t.Fatal(err)
	}
	if answer := autoModeUnixCall(t, w, string(payload)); !answer.Ok {
		t.Fatalf("inject %s: %s", pr.ID, protocol.Deref(answer.Error))
	}
}

func prFlowAwait(app *testworld.Peer, id string, match func(protocol.PR) bool) protocol.PR {
	app.T.Helper()
	updated := testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool {
		return slices.ContainsFunc(e.Prs, func(pr protocol.PR) bool { return pr.ID == id && match(pr) })
	})
	return updated.Prs[slices.IndexFunc(updated.Prs, func(pr protocol.PR) bool { return pr.ID == id })]
}
