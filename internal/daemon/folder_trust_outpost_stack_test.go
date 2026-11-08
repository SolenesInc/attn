package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAClaudeStoppedAtTheFolderTrustQuestionOnAnOutpostWaitsForTheUser(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
	outpost.AskFolderTrustAtNextBoot()
	id := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Host = protocol.Ptr(pair.Host)
	})
	app.AwaitScreen(id, folderTrustChoice)

	outpost.Advance(10 * time.Second)

	testworld.AwaitSession(app, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.StateReason) == "folder_trust_question"
	})
}

func TestADelegationToAnOutpostRecordsTrustOnTheOutpostForARepositoryKnownFromTheHome(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return slices.ContainsFunc(m.Outposts, func(row protocol.OutpostInfo) bool {
			return row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync
		})
	})
	homeRepo := newRepoAt(t, "shop-home", shopRemote)
	known := home.Spawn(app, fakeagent.Claude, homeRepo)
	home.Launched(known)
	testworld.AwaitSession(app, known, func(s protocol.Session) bool {
		return protocol.Deref(s.ConversationStarted) && protocol.Deref(s.RepositoryOrigin) != ""
	})
	repo := newRepoAt(t, "shop-outpost", shopRemote)

	outpost.AskFolderTrustAtNextBoot()
	request := claudeBrief(repo, "outpost", reuseMain())
	request.Host = protocol.Ptr(pair.Host)
	result, err := home.Client().Delegate(request)
	if err != nil {
		t.Fatalf("delegating: %v", err)
	}
	if said := protocol.Deref(result.FirstTurnUnconfirmed); said != "" {
		t.Errorf("the first turn went unconfirmed: %s", said)
	}
	if !outpost.ClaudeFolderTrusted(repo) {
		t.Errorf("the outpost's Claude config does not trust %s", repo)
	}
	if home.ClaudeFolderTrusted(repo) {
		t.Errorf("the home's Claude config trusts %s, want the outpost's alone", repo)
	}
}
