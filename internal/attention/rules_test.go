package attention

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func age(d time.Duration) *Age {
	a := Age(d)
	return &a
}

var nothingRunning = Background{Reported: true}

// The rows are points of the facts eval (facts_dataset_v4.jsonl, by id), rewritten without transcript text.
var ruleRows = []struct {
	point   string
	rule    string
	verdict Kind
	label   Label
	facts   Facts
}{
	{"work-FnGhmPVYZg", "tool_open", Open, LabelAsksYou, Facts{
		State: protocol.SessionStatePendingApproval, RunStartedBy: RunStartedByNotification, TurnOwed: true,
		OpenTool: &Tool{Reason: "AskUserQuestion", OpenFor: Age(20 * time.Minute)}, Background: nothingRunning,
		Launch: LaunchedByDelegation, ResultGoesTo: ResultToDispatcher, Stake: &Seed{ID: "s-5qqtaf", Status: "growing"},
		Delegates: []Delegate{{Session: "8cd31fbd", Stake: "s-2r078a", Live: true, ReportsHere: true}},
	}},
	{"pos-71996cb6-r17", "turn_owed", Open, LabelUnreadReport, Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByNotification, TurnOwed: true, Background: nothingRunning,
		Launch: LaunchedByDelegation, ResultGoesTo: ResultToDispatcher, Stake: &Seed{ID: "s-k62chx", Status: "harvested", ChecksDone: 4, ChecksTotal: 4},
	}},
	{"pos-7742f723-r2", "ask_open", Open, LabelAsksYou, Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByUser,
		Background: Background{Reported: true, Tasks: []Task{{Kind: "subagent", AgentType: "general-purpose", WakesAgent: "exit", Age: Age(3 * time.Second)}}},
		Wakes:      []Wake{{Kind: WakeAsk, Subject: "a-cgfbb1", SentSinceTurn: true, Waited: age(18 * time.Second)}},
		Launch:     LaunchedByUser, ResultGoesTo: ResultToUser,
	}},
	{"pos-6d773479-r20", "delegate_finished", Finished, LabelFinished, Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByNotification, Background: nothingRunning,
		Launch: LaunchedByDelegation, ResultGoesTo: ResultToDispatcher, Stake: &Seed{ID: "s-mr3a3z", Status: "harvested"},
		Wakes: []Wake{{Kind: WakeWatchedSeed, Seed: "s-cqxcw4", SeedStatus: "growing"}},
	}},
	{"pos-c8793d4c-r2", "background_young", Quiet, "", Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByNotification,
		Background: Background{Reported: true, Tasks: []Task{{Kind: "shell", WakesAgent: "exit", Age: Age(3 * time.Second)}}},
		Launch:     LaunchedByDelegation, ResultGoesTo: ResultToNobody, Stake: &Seed{ID: "s-n6t5yj", Status: "harvested", ChecksDone: 5, ChecksTotal: 5},
	}},
	{"pos-14087774-r21", "wake_registered", Quiet, "", Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByNotification, Background: nothingRunning,
		Launch: LaunchedByDelegation, ResultGoesTo: ResultToNobody, Stake: &Seed{ID: "s-gt54ef", Status: "harvested"},
		Delegates: []Delegate{{Session: "968dbe2e", Stake: "s-0gyggj", Live: true, ReportsHere: true}},
	}},
	{"pos-c8793d4c-r1", "", Unsure, "", Facts{
		State: protocol.SessionStateIdle, RunStartedBy: RunStartedByNotification, Background: nothingRunning,
		Launch: LaunchedByDelegation, ResultGoesTo: ResultToNobody, Stake: &Seed{ID: "s-n6t5yj", Status: "harvested", ChecksDone: 5, ChecksTotal: 5},
	}},
}

func TestRulesDecideEachEvalPointFromFactsAlone(t *testing.T) {
	covered := map[string]bool{}
	for _, row := range ruleRows {
		wantDecider := DeciderNone
		if row.rule != "" {
			wantDecider = "rule:" + row.rule
			covered[row.rule] = true
		}
		for _, message := range []string{"", "Landed it. The fix is on main and the seed is harvested."} {
			facts := row.facts
			facts.LastMessage = message
			if got := Rules(facts); got.Kind != row.verdict || got.Decider != wantDecider || got.Label != row.label {
				t.Errorf("%s with message %q: got %s, want %s (%s) by %s", row.point, message, got, row.verdict, row.label, wantDecider)
			}
		}
	}
	for _, name := range RuleNames() {
		if !covered[name] {
			t.Errorf("no row covers rule %s", name)
		}
	}
}

func TestRulesNeverTrustUnknownBackground(t *testing.T) {
	finished := ruleRows[3].facts
	finished.Background = Background{}
	if got := Rules(finished); got.Kind == Finished {
		t.Errorf("a delegate whose harness did not say what it left running was judged %s", got)
	}
	young := ruleRows[4].facts
	young.Background.Reported = false
	if got := Rules(young); got.Decider == "rule:background_young" {
		t.Errorf("unreported tasks were judged %s", got)
	}
}
