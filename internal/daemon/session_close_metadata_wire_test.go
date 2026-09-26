package daemon_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASeedRemembersWhereAndHowItsClosedOrReapedTenderRan(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	type tender struct {
		repo, cwd, seed string
		pane            sessionPane
		run             *fakeagent.Run
	}
	tenders := map[string]*tender{}
	for name, subdir := range map[string]string{"closed": "web", "reaped": ""} {
		repo := newRepo(t, name)
		cwd := filepath.Join(repo, subdir)
		spawned, workspace, pane := w.RequestSpawn(app, fakeagent.Codex, cwd)
		run := w.Launched(spawned.ID)
		app.TypeLine(spawned.ID, "fix the "+name+" checkout")
		run.Prompted()
		run.Reply("Fixed. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, spawned.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		seed := plantSeedAs(t, cli, spawned.ID, "fix the "+name+" checkout")
		if _, err := cli.SeedTransition(spawned.ID, seed, "tend", "", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("%s tends %s: %v", spawned.ID, seed, err)
		}
		tenders[name] = &tender{repo: repo, cwd: cwd, seed: seed, pane: sessionPane{session: spawned.ID, workspace: workspace, pane: pane}, run: run}
	}

	closed := tenders["closed"]
	closePane(app, closed.pane)
	awaitClosed(app, closed.pane.session)

	reaped := tenders["reaped"]
	runGit(t, reaped.repo, "checkout", "-b", "feature/reaped")
	reaped.run.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == reaped.pane.session })
	removeReopenRollout(t, w, reaped.run.ConversationID)
	w.restart()
	w.App()
	cli = w.Client()

	for name, want := range map[string]struct {
		subdir, branch, conversation string
	}{
		"closed": {"web", "main", closed.run.ConversationID},
		"reaped": {"", "feature/reaped", ""},
	} {
		tender := tenders[name]
		shown, err := cli.SeedShow("", tender.seed)
		if err != nil {
			t.Fatalf("seed show %s: %v", tender.seed, err)
		}
		ran := shown.Seed.Continuation
		if ran == nil {
			t.Errorf("the %s tender's seed kept no record of where it ran", name)
			continue
		}
		if ran.Cwd != tender.cwd || ran.Agent != string(fakeagent.Codex) || protocol.Deref(ran.RepositoryRoot) != tender.repo ||
			protocol.Deref(ran.RepositorySubdir) != want.subdir || protocol.Deref(ran.Branch) != want.branch ||
			protocol.Deref(ran.NativeConversationID) != want.conversation {
			t.Errorf("the %s tender ran in %q with %s on %q in repository %q under %q resuming %q; want %q, codex, %q, %q, %q, %q", name,
				ran.Cwd, ran.Agent, protocol.Deref(ran.Branch), protocol.Deref(ran.RepositoryRoot), protocol.Deref(ran.RepositorySubdir),
				protocol.Deref(ran.NativeConversationID), tender.cwd, want.branch, tender.repo, want.subdir, want.conversation)
		}
	}
}
