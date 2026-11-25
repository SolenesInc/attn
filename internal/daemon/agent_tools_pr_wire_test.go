package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEveryPullRequestCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))
	seed := plantForTools(t, w)
	pull, other := "https://github.com/acme/shop/pull/7", "https://github.com/acme/shop/pull/8"

	shapes.call("pr_record", map[string]any{"url": pull})
	shapes.call("pr_watch", map[string]any{"url": pull, "mode": "codex"})
	shapes.call("pr_watch", map[string]any{"url": other, "seed": seed})
	var status protocol.PullRequestListResult
	data, _ := json.Marshal(shapes.call("pr_status", map[string]any{"url": pull}).StructuredContent)
	if err := json.Unmarshal(data, &status); err != nil || status.SessionID != protocol.SessionID(session) || len(status.PullRequests) != 1 || protocol.Deref(status.PullRequests[0].WatchMode) != protocol.PullRequestWatchModeCodex {
		t.Errorf("pr_status of the watched pull request = %s (%v), want it alone, watched in codex mode for the caller", data, err)
	}
	shapes.call("pr_ls", map[string]any{"seed": seed})
	shapes.call("pr_unwatch", map[string]any{"url": other, "seed": seed})
	shapes.call("pr_forget", map[string]any{"url": pull})
	shapes.finish("pr_")

	refused := agent.mustCall(t, "pr_watch", map[string]any{"url": pull, "reviewer": "octocat"})
	if text := resultText(refused); !refused.IsError || text != "reviewer_without_formal_review: reviewer is only valid with mode formal-review\nFix: Pass reviewer together with mode formal-review, or leave it out." {
		t.Errorf("pr_watch with a reviewer in green mode answered %q, want the catalog's refusal naming the args, with its fix", text)
	}
}

func TestAPullRequestCallerSeesAndWatchesNothingOfAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	seed := plantForTools(t, w)
	pull := "https://github.com/acme/shop/pull/7"
	var watched protocol.PullRequestWatchResult
	if err := w.Client().RunCommand("pr watch", protocol.SessionID(home), map[string]any{"url": pull, "seed": seed}, &watched); err != nil {
		t.Fatal(err)
	}

	sideApp := w.AppOn(createProfile(app, "Side").ID)
	side := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(side))}
	notHere := "no seed " + seed + " is planted here"
	for _, tc := range []struct {
		tool, command string
		args          map[string]any
	}{
		{"pr_ls", "pr ls", map[string]any{"seed": seed}},
		{"pr_status", "pr status", map[string]any{"seed": seed}},
		{"pr_watch", "pr watch", map[string]any{"url": pull, "seed": seed}},
		{"pr_unwatch", "pr unwatch", map[string]any{"url": pull, "seed": seed}},
	} {
		if refused := agent.mustCall(t, tc.tool, tc.args); !refused.IsError || !strings.Contains(resultText(refused), notHere) {
			t.Errorf("%s %v from the Side profile's session answered %s, want %q", tc.tool, tc.args, resultText(refused), notHere)
		}
		var answer map[string]any
		if err := w.Client().RunCommand(tc.command, protocol.SessionID(side), tc.args, &answer); err == nil || !strings.Contains(err.Error(), notHere) {
			t.Errorf("run_command %s %v as the Side profile's session answered %v, %v, want %q", tc.command, tc.args, answer, err, notHere)
		}
	}
	listed := agent.mustCall(t, "pr_ls", map[string]any{})
	if text := resultText(listed); listed.IsError || strings.Contains(text, pull) {
		t.Errorf("pr_ls from the Side profile's session answered %s, want none of the Default profile's pull requests", text)
	}
	var stillWatched protocol.PullRequestListResult
	if err := w.Client().RunCommand("pr ls", protocol.SessionID(home), map[string]any{"seed": seed}, &stillWatched); err != nil || len(stillWatched.PullRequests) != 1 {
		t.Errorf("the Default profile's seed watches %+v (%v) after the Side profile's calls, want its one watch untouched", stillWatched.PullRequests, err)
	}
}
