package daemon_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

type attentionFacts struct {
	State                string `json:"state"`
	RunStartedBy         string `json:"run_started_by"`
	TurnOwed             bool   `json:"turn_owed"`
	ReportUnread         bool   `json:"report_unread"`
	UserFocusedAfterStop bool   `json:"user_focused_after_stop"`
	Launch               string `json:"launch"`
	ResultGoesTo         string `json:"result_goes_to"`
	Stake                *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"stake"`
	Delegates []struct {
		Session      string `json:"session"`
		Stake        string `json:"stake"`
		Live         bool   `json:"live"`
		OwesUserTurn bool   `json:"owes_user_turn"`
		ReportsHere  bool   `json:"reports_here"`
	} `json:"delegates"`
	Background backgroundFacts `json:"background"`
	Wakes      []struct {
		Kind string `json:"kind"`
		Seed string `json:"seed"`
	} `json:"wakes"`
	WatchedPRs []struct {
		URL string `json:"url"`
		CI  string `json:"ci"`
	} `json:"watched_prs"`
}

type backgroundFacts []struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	WakesAgent string `json:"wakes_agent"`
	Age        string `json:"age"`
}

func (b *backgroundFacts) UnmarshalJSON(data []byte) error {
	if string(data) == `"unknown"` {
		*b = nil
		return nil
	}
	type tasks backgroundFacts
	return json.Unmarshal(data, (*tasks)(b))
}

func attentionFactsOf(t *testing.T, cli *client.Client, session string) (attentionFacts, string) {
	t.Helper()
	explained, err := cli.StateExplain(session)
	if err != nil {
		t.Fatalf("state explain %s: %v", session, err)
	}
	encoded, err := json.Marshal(explained.Facts)
	if err != nil {
		t.Fatal(err)
	}
	var facts attentionFacts
	if err := json.Unmarshal(encoded, &facts); err != nil {
		t.Fatalf("facts of %s %s: %v", session, encoded, err)
	}
	return facts, string(encoded)
}

func TestAttentionFactsSayAFinishedDelegatesResultReachesItsLiveDispatcher(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the first table", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	delegate := string(delegated.SessionID)
	run := w.Launched(delegate)
	run.Prompted()
	run.Reply("Users or orders first? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, delegate, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	dispatcher, raw := attentionFactsOf(t, cli, "caller")
	if len(dispatcher.Delegates) != 1 || dispatcher.Delegates[0].Session != delegate || dispatcher.Delegates[0].Stake != delegated.SeedID ||
		!dispatcher.Delegates[0].Live || !dispatcher.Delegates[0].OwesUserTurn || !dispatcher.Delegates[0].ReportsHere {
		t.Errorf("the dispatcher's facts are %s, want its one live delegate on %s owing the user a turn and reporting here", raw, delegated.SeedID)
	}

	moveDelegationSeed(t, cli, delegate, delegated.SeedID, "harvest", "migrated")
	facts, raw := attentionFactsOf(t, cli, delegate)
	if facts.Launch != "delegation" || facts.ResultGoesTo != "dispatcher" || facts.Stake == nil || facts.Stake.ID != delegated.SeedID || facts.Stake.Status != "harvested" {
		t.Errorf("the finished delegate's facts are %s, want a delegation whose harvested stake reports to its dispatcher", raw)
	}

	if _, err := cli.SeedWatch("caller", delegated.SeedID, true); err != nil {
		t.Fatal(err)
	}
	if facts, raw := attentionFactsOf(t, cli, delegate); facts.ResultGoesTo != "nobody" {
		t.Errorf("with its dispatcher no longer watching, the delegate's facts are %s, want its result going to nobody", raw)
	}
}

func TestAttentionFactsSayWhetherTheUserFocusedTheAgentAfterItStopped(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		claude := w.bubbleClaude(t, app, "shop")
		app.TypeLine(claude.id, "rename the module")
		w.advance(0)
		if err := cli.UpdateState(protocol.TerminalID(claude.self), protocol.StateIdle); err != nil {
			t.Fatal(err)
		}
		w.advance(time.Minute)
		if facts, raw := attentionFactsOf(t, cli, claude.id); facts.UserFocusedAfterStop || !facts.ReportUnread {
			t.Errorf("before the user looks, the facts are %s, want no focus after the stop and the report unread", raw)
		}
		app.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: protocol.TerminalID(claude.self)})
		w.advance(0)
		if facts, raw := attentionFactsOf(t, cli, claude.id); !facts.UserFocusedAfterStop || facts.ReportUnread {
			t.Errorf("after the user focused the agent, the facts are %s, want focus after the stop and the report read", raw)
		}
	})
}

func TestAttentionFactsAgeABackgroundTaskFromTheToolCallThatLaunchedIt(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		terminal := protocol.TerminalID(w.Terminal("s1"))
		if err := cli.RecordBackgroundTaskStarted(terminal, "bdeploy"); err != nil {
			t.Fatal(err)
		}
		w.advance(25 * time.Minute)
		deploy := protocol.BackgroundTask{ID: protocol.Ptr("bdeploy"), Kind: protocol.Ptr("shell"), Description: protocol.Ptr("Deploy staging"), Command: protocol.Ptr("./deploy.sh staging"), Wakes: protocol.Ptr(protocol.BackgroundTaskWakeExit)}
		unlaunched := protocol.BackgroundTask{ID: protocol.Ptr("bwatch"), Kind: protocol.Ptr("shell"), Command: protocol.Ptr("gh run watch")}
		if err := cli.SendStop(terminal, "", client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy, unlaunched}}); err != nil {
			t.Fatal(err)
		}
		w.advance(5 * time.Minute)
		facts, raw := attentionFactsOf(t, cli, "s1")
		if len(facts.Background) != 2 || facts.Background[0].ID != "bdeploy" || facts.Background[0].Age != "30m0s" ||
			facts.Background[0].Command != "./deploy.sh staging" || facts.Background[0].WakesAgent != "exit" || facts.Background[1].Age != "5m0s" {
			t.Fatalf("the facts are %s, want the deploy aged 30m from its launch and the other task 5m from the stop that listed it", raw)
		}

		if err := cli.SendStop(terminal, "", client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy}}); err != nil {
			t.Fatal(err)
		}
		w.advance(10 * time.Minute)
		if facts, raw := attentionFactsOf(t, cli, "s1"); len(facts.Background) != 1 || facts.Background[0].Age != "40m0s" {
			t.Errorf("a later stop listing the deploy leaves the facts %s, want it aged 40m from its launch", raw)
		}

		if err := cli.SendStop(terminal, "", client.StopFacts{}); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if facts, raw := attentionFactsOf(t, cli, "s1"); len(facts.Background) != 0 {
			t.Errorf("a stop that lists nothing leaves the facts %s, want no background task", raw)
		}
	})
}

func TestAttentionFactsDropBackgroundTasksWhenTheAgentReloads(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		claude := w.bubbleClaude(t, app, "shop")
		terminal := protocol.TerminalID(claude.self)
		deploy := protocol.BackgroundTask{ID: protocol.Ptr("bdeploy"), Kind: protocol.Ptr("shell"), Command: protocol.Ptr("./deploy.sh staging")}
		if err := cli.SendStop(terminal, "", client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy}}); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if facts, raw := attentionFactsOf(t, cli, claude.id); len(facts.Background) != 1 {
			t.Fatalf("after the stop, the facts are %s, want the deploy", raw)
		}
		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(claude.id), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == claude.id })
		if !reloaded.Success {
			t.Fatalf("reloading %s: %s", claude.id, protocol.Deref(reloaded.Error))
		}
		w.bootBubbleClaude(t, claude.id)
		if facts, raw := attentionFactsOf(t, cli, claude.id); len(facts.Background) != 0 {
			t.Errorf("after a reload ended the process that ran the deploy, the facts are %s, want no background task", raw)
		}
	})
}

func TestARestartKeepsTheBackgroundTasksAndRunStarterOfAWaitingAgent(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "queue_mode_enabled", "true")
		registerSessions(t, w, cli, "peer")
		claude := w.bubbleClaude(t, app, "shop")
		if booted := queriedSession(t, cli, claude.id); protocol.Deref(booted.TurnOwed) {
			settleFromApp(app, booted)
		}
		sendAgentMessage(t, cli, "peer", claude.id, "deploy staging when you can")
		w.advance(sessionmonitor.QuietWindow)
		pasted := claude.term.Pasted()
		if len(pasted) != 1 {
			t.Fatalf("attn pasted %q, want one doorbell", pasted)
		}
		claude.take(pasted[0])
		readInbox(t, cli, claude.id, 0)
		claude.transcript.Answer("Deploying staging; I'll report when it ends. <!-- attn:state=idle -->")
		deploy := protocol.BackgroundTask{ID: protocol.Ptr("bdeploy"), Kind: protocol.Ptr("shell"), Description: protocol.Ptr("Deploy staging"), Command: protocol.Ptr("./deploy.sh staging"), Wakes: protocol.Ptr(protocol.BackgroundTaskWakeExit)}
		if err := cli.SendStop(protocol.TerminalID(claude.self), claude.transcript.Path, client.StopFacts{BackgroundTasks: []protocol.BackgroundTask{deploy}}); err != nil {
			t.Fatal(err)
		}
		w.advance(10 * time.Minute)
		waiting := func(phase string) {
			t.Helper()
			facts, raw := attentionFactsOf(t, w.Client(), claude.id)
			if len(facts.Background) != 1 || facts.Background[0].ID != "bdeploy" || facts.Background[0].Age != "10m0s" || facts.RunStartedBy != "notification" {
				t.Errorf("%s, the facts are %s, want the deploy aged 10m and the run started by the doorbell", phase, raw)
			}
		}
		waiting("before the restart")
		w.restart()
		waiting("after the restart")
	})
}

func TestAttentionFactsCarryTheCIStateOfAWatchedPullRequest(t *testing.T) {
	serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", shopPull(71))
		w.advance(protocol.HeatHotInterval)
		if facts, raw := attentionFactsOf(t, cli, "s1"); len(facts.WatchedPRs) != 0 {
			t.Errorf("before the watch, the facts are %s, want no watched pull request", raw)
		}
		if err := cli.WatchSessionPullRequest("s1", "", shopPull(71), protocol.PullRequestWatchModeGreen, ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		ci := protocol.Deref(onlyPullRequest(t, cli, "s1").CIStatus)
		if facts, raw := attentionFactsOf(t, cli, "s1"); ci == "" || len(facts.WatchedPRs) != 1 || facts.WatchedPRs[0].URL != shopPull(71) || facts.WatchedPRs[0].CI != ci {
			t.Errorf("the facts are %s, want the watched pull request with the CI state %q GitHub reported", raw, ci)
		}
	})
}

func TestAttentionFactsCountAWatchedSeedAsAWakeOnlyWhileItIsOpen(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	registerSessions(t, w, cli, "watcher", "builder")
	seed := plantSeedAs(t, cli, "builder", "Migrate the first table")
	if _, err := cli.SeedWatch("watcher", seed, false); err != nil {
		t.Fatal(err)
	}
	if facts, raw := attentionFactsOf(t, cli, "watcher"); len(facts.Wakes) != 1 || facts.Wakes[0].Kind != "watched_seed" || facts.Wakes[0].Seed != seed {
		t.Fatalf("while %s grows, the watcher's facts are %s, want it as the one wake", seed, raw)
	}
	moveDelegationSeed(t, cli, "builder", seed, "harvest", "migrated")
	if facts, raw := attentionFactsOf(t, cli, "watcher"); len(facts.Wakes) != 0 {
		t.Errorf("after %s was harvested, the watcher's facts are %s, want no wake", seed, raw)
	}
}
