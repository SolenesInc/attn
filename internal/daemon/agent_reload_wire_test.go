package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentReloadAuthorizesEveryTargetInTheDelegationChain(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		parent := w.Spawn(app, fakeagent.Claude, w.Path("parent"))
		other := w.Spawn(app, fakeagent.Claude, w.Path("other"))
		shell := w.Spawn(app, shellHarness, w.Path("shell"))
		for range 2 {
			w.Spawn(app, fakeagent.Claude, w.Path("dupe"), withIDPrefix("dupe-"))
		}
		delegate := func(caller, title string) string {
			t.Helper()
			seed := plantDelegationSeed(t, cli, caller, title)
			msg := delegateAtSeed(caller, w.Path("parent"), seed)
			msg.Agent = protocol.Ptr("claude")
			result, err := cli.Delegate(msg)
			if err != nil {
				t.Fatal(err)
			}
			return string(result.SessionID)
		}
		child := delegate(parent, "Build the field")
		grandchild := delegate(child, "Review the field")
		for _, pair := range [][2]string{{child, parent}, {other, child}} {
			result, err := cli.AgentReload([]string{pair[1]}, protocol.SessionID(pair[0]), false)
			if err != nil || len(result.Outcomes) != 1 || result.Outcomes[0].Success || !strings.Contains(protocol.Deref(result.Outcomes[0].Error), "delegation-chain rule") {
				t.Fatalf("reload %v = %+v, %v; want a delegation-chain refusal", pair, result, err)
			}
		}
		if _, err := cli.AgentReload(nil, protocol.SessionID(parent), true); err == nil || !strings.Contains(err.Error(), "chief") {
			t.Fatalf("ordinary agent reload --all = %v", err)
		}
		if _, err := cli.AgentReload([]string{child}, "ghost", false); err == nil {
			t.Fatal("reload accepted an unknown caller")
		}
		result, err := cli.AgentReload([]string{parent, other, child[:8], "missing", grandchild, "dupe-", shell, child}, protocol.SessionID(parent), false)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Outcomes) != 7 || protocol.Deref(result.Outcomes[len(result.Outcomes)-1].SessionID) != protocol.SessionID(parent) {
			t.Fatalf("reload = %+v; want seven outcomes with the caller last and duplicate child reloaded once", result)
		}
		succeeded := []string{}
		for _, outcome := range result.Outcomes {
			if outcome.Success {
				id := string(protocol.Deref(outcome.SessionID))
				succeeded = append(succeeded, id)
				terminal := app.Terminal(id)
				testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
			} else if protocol.Deref(outcome.Error) == "" {
				t.Errorf("refusal has no explanation: %+v", outcome)
			}
		}
		if !slices.Equal(agentCloseSortedIDs(succeeded), agentCloseSortedIDs([]string{parent, child, grandchild})) {
			t.Fatalf("reloaded %v; want only self and descendants", succeeded)
		}
		for _, event := range app.Received() {
			if event.Event == protocol.EventRuntimeRespawned && protocol.Deref(event.ID) == app.Terminal(other) {
				t.Fatal("refused peer was reloaded")
			}
		}
	})
}

func TestAgentReloadAllIncludesChiefAndCrewButOnlyInTheirProfile(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		chief := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
		worker := w.Spawn(app, fakeagent.Claude, w.Path("worker"), withIDPrefix("shared-"))
		crew := string(wakeCrew(t, cli, "trellis", "").SessionID)
		w.Spawn(app, shellHarness, w.Path("shell"))
		profile := createProfile(app, "Other profile")
		foreignApp := w.AppOn(profile.ID)
		foreign := w.Spawn(foreignApp, fakeagent.Claude, w.Path("foreign"), withIDPrefix("shared-"))
		refused, err := cli.AgentReload([]string{foreign}, protocol.SessionID(chief), false)
		if err != nil || len(refused.Outcomes) != 1 || refused.Outcomes[0].Success {
			t.Fatalf("cross-profile reload = %+v, %v", refused, err)
		}
		prefix, err := cli.AgentReload([]string{"shared-"}, protocol.SessionID(chief), false)
		if err != nil || len(prefix.Outcomes) != 1 || !prefix.Outcomes[0].Success || protocol.Deref(prefix.Outcomes[0].SessionID) != protocol.SessionID(worker) {
			t.Fatalf("profile-local prefix reload = %+v, %v", prefix, err)
		}
		if _, err := cli.AgentReload([]string{worker}, protocol.SessionID(chief), true); err == nil {
			t.Fatal("reload accepted both --all and explicit targets")
		}
		result, err := cli.AgentReload(nil, protocol.SessionID(chief), true)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Outcomes) != 3 || protocol.Deref(result.Outcomes[2].SessionID) != protocol.SessionID(chief) {
			t.Fatalf("reload --all = %+v; want worker, crew and chief last", result)
		}
		for _, outcome := range result.Outcomes {
			if !outcome.Success || !slices.Contains([]string{chief, worker, crew}, string(protocol.Deref(outcome.SessionID))) {
				t.Fatalf("reload --all outcome = %+v", outcome)
			}
			terminal := app.Terminal(string(protocol.Deref(outcome.SessionID)))
			testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
		}
	}, fakeagent.Claude)
}
