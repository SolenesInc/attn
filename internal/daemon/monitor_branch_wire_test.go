package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionsBranchFollowsItsCheckout(t *testing.T) {
	repo := t.TempDir()
	gitRepo(t, repo, "checkout-fix")
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, repo)
		if got := protocol.Deref(queriedSession(t, w.Client(), session).Branch); got != "main" {
			t.Fatalf("launched branch = %q, want main", got)
		}
		runGit(t, repo, "checkout", "checkout-fix")
		w.advance(15 * time.Second)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return protocol.Deref(s.Branch) == "checkout-fix"
		})
		if got := protocol.Deref(queriedSession(t, w.Client(), session).Branch); got != "checkout-fix" {
			t.Fatalf("reported branch = %q, want checkout-fix", got)
		}
	})
}
