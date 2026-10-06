package daemon_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestADelegationWhoseAgentReportsNoTurnReturnsAtTheTripwireNamingPeek(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		cwd := w.Path("api")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		request := brief(cwd, "Say hello")
		request.Agent = protocol.Ptr("claude")
		request.Label = protocol.Ptr("hello")

		asked := time.Now()
		result, err := cli.Delegate(request)
		if err != nil {
			t.Fatalf("delegating to a silent agent: %v", err)
		}
		if waited := time.Since(asked); waited < 90*time.Second || waited > 91*time.Second {
			t.Errorf("the delegation returned after %s, want the 90s tripwire", waited)
		}
		note := protocol.Deref(result.FirstTurnUnconfirmed)
		for _, want := range []string{"no turn reported by the agent within 1m30s", string("attn agent peek " + result.SessionID[:8])} {
			if !strings.Contains(note, want) {
				t.Errorf("the unconfirmed first turn reads %q, want it to say %q", note, want)
			}
		}
		if result.FirstTurnAt != nil {
			t.Errorf("a silent agent was credited with a first turn at %s", *result.FirstTurnAt)
		}
	})
}

func TestADelegationTripwireCountsFromTheLaunchAcrossADaemonRestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cwd := w.Path("api")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		request := brief(cwd, "Say hello")
		request.RequestID = "tripwire-across-restart"
		request.Agent = protocol.Ptr("claude")
		request.Label = protocol.Ptr("hello")

		interrupted := w.Client()
		go func() { _, _ = interrupted.Delegate(request) }()
		w.advance(60 * time.Second)
		w.restart()

		asked := time.Now()
		result, err := w.Client().Delegate(request)
		if err != nil {
			t.Fatalf("delegating again after the restart: %v", err)
		}
		if waited := time.Since(asked); waited < 30*time.Second || waited > 31*time.Second {
			t.Errorf("after the restart the delegation returned in %s, want the 30s left of its 90s tripwire", waited)
		}
		if result.FirstTurnUnconfirmed == nil {
			t.Errorf("the delegation = %+v, want its first turn unconfirmed", result)
		}
	})
}
