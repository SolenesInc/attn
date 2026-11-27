package attention

import (
	"fmt"
	"slices"
	"time"
)

type Kind string

const (
	Open     Kind = "open"
	Quiet    Kind = "quiet"
	Finished Kind = "finished"
	Unsure   Kind = "unsure"
)

type Verdict struct {
	Kind    Kind          `json:"verdict"`
	Decider string        `json:"decider"`
	Label   Label         `json:"label,omitempty"`
	Reason  string        `json:"reason"`
	Failure Failure       `json:"failure,omitempty"`
	Latency time.Duration `json:"-"`
}

const DeciderNone = "none"

type Label string

const (
	LabelUnreadReport   Label = "unread report"
	LabelAsksYou        Label = "asks you"
	LabelNothingWakesIt Label = "nothing wakes it"
	LabelStuckWait      Label = "stuck wait"
	LabelFinished       Label = "finished"
)

var Labels = []Label{LabelUnreadReport, LabelAsksYou, LabelNothingWakesIt, LabelStuckWait, LabelFinished}

// 22 of 22 background waits judged under 30 min were quiet (round 2), and 149 of 1,923 background waits
// ran past 20 min (stuck_working.md), where the first elapsed checkpoint looks at them.
const youngBackgroundTask = 20 * time.Minute

type rule struct {
	name   string
	kind   Kind
	label  Label
	reason string
	holds  func(Facts) bool
}

var rules = []rule{
	{"tool_open", Open, LabelAsksYou, "a question or an approval waits on the user", func(f Facts) bool { return f.OpenTool != nil }},
	{"turn_owed", Open, LabelUnreadReport, "the agent's turn is already open to the user", func(f Facts) bool { return f.TurnOwed }},
	{"ask_open", Open, LabelAsksYou, "the agent sent an ask since its turn last opened or settled", sentAnAsk},
	{"delegate_finished", Finished, LabelFinished, "the stake is done, nothing runs, and the dispatcher watches it", delegateFinished},
	{"background_young", Quiet, "", "a background task started under 20 min ago", youngBackground},
	{"wake_registered", Quiet, "", "something registered will wake the agent", wakeRegistered},
}

func Rules(f Facts) Verdict {
	for _, r := range rules {
		if r.holds(f) {
			return Verdict{Kind: r.kind, Decider: "rule:" + r.name, Label: r.label, Reason: r.reason}
		}
	}
	return Verdict{Kind: Unsure, Decider: DeciderNone, Reason: "no rule decides"}
}

func RuleNames() []string {
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = r.name
	}
	return names
}

func sentAnAsk(f Facts) bool {
	return slices.ContainsFunc(f.Wakes, func(w Wake) bool { return w.Kind == WakeAsk && w.SentSinceTurn })
}

func delegateFinished(f Facts) bool {
	return f.Launch == LaunchedByDelegation && f.Stake != nil && seedDone(f.Stake.Status) &&
		f.Background.None() && !slices.ContainsFunc(f.Delegates, func(d Delegate) bool { return d.Live }) &&
		f.ResultGoesTo == ResultToDispatcher
}

func youngBackground(f Facts) bool {
	return f.Background.Reported && slices.ContainsFunc(f.Background.Tasks, func(t Task) bool { return time.Duration(t.Age) < youngBackgroundTask })
}

func wakeRegistered(f Facts) bool {
	if slices.ContainsFunc(f.Delegates, func(d Delegate) bool { return d.Live && d.ReportsHere }) {
		return true
	}
	if slices.ContainsFunc(f.WatchedPRs, func(pr PR) bool { return pr.State == "open" }) {
		return true
	}
	return slices.ContainsFunc(f.Wakes, func(w Wake) bool {
		switch w.Kind {
		case WakeStandby, WakeReminder, WakeSnooze:
			return true
		case WakeWatchedSeed:
			return w.TenderLive != nil && *w.TenderLive
		default:
			return false
		}
	})
}

func seedDone(status string) bool {
	return status == "harvested" || status == "withered"
}

func (v Verdict) String() string {
	kind := string(v.Kind)
	if v.Label != "" {
		kind += fmt.Sprintf(" (%s)", v.Label)
	}
	return fmt.Sprintf("%s by %s: %s", kind, v.Decider, v.Reason)
}
