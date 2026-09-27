package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASeedWhoseDelegationIsStillStartingRefusesASecondDelegation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	seed := plantDelegationSeed(t, cli, "caller", "Open plot")
	first := delegateAtSeed("caller", cwd, seed)
	first.RequestID, first.Label = "first", protocol.Ptr("first")
	boot := w.HoldNextBoot()
	accepted, err := cli.StartDelegation(first)
	if err != nil {
		t.Fatal(err)
	}

	second := delegateAtSeed("caller", cwd, seed)
	second.Label = protocol.Ptr("second")
	if result, err := cli.Delegate(second); err == nil || !strings.Contains(err.Error(), "already has a delegation being prepared") {
		t.Errorf("a second delegation of %s while the first is starting = %+v, %v; want it refused", seed, result, err)
	}
	boot()
	if result, err := cli.Delegate(first); err != nil || result.SessionID != accepted.SessionID || result.SeedID != seed {
		t.Fatalf("the first delegation = %+v, %v; want it to finish on %s", result, err, seed)
	}
}
