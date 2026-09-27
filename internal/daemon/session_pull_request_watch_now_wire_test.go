package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
)

func TestANewWatcherLearnsItsPullRequestsReadinessAtOnceWhileAnotherWaitsOnCodex(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.codexThumbsUp = true })
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1", "s2")
		w.advance(protocol.HeatHotInterval / 3)
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeCodex)
		w.advance(time.Second)
		if pr := onlyPullRequest(t, cli, "s1"); protocol.Deref(pr.ReadinessReason) != "codex_settling" {
			t.Fatalf("s1's pull request = %+v, want it waiting for Codex to settle", pr)
		}

		watchPullRequestOn(t, cli, "s2", protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		if pr := onlyPullRequest(t, cli, "s2"); protocol.Deref(pr.ReadinessState) != prreadiness.StateReady {
			t.Errorf("a second before s1's Codex deadline s2's green watch = %+v, want it ready", pr)
		}
	})
}
