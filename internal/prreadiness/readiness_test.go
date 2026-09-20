package prreadiness

import (
	"testing"
	"time"
)

func greenObservation() Observation {
	return Observation{State: "open", HeadSHA: "head-a", MergeStateStatus: "CLEAN", FeedbackComplete: true}
}

func TestEvaluateModes(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		mode        Mode
		reviewer    string
		observation Observation
		cursor      Cursor
		wantState   string
		wantReason  string
	}{
		{name: "green default", mode: ModeGreen, observation: greenObservation(), wantState: StateReady, wantReason: "green"},
		{name: "hooks are green", mode: ModeGreen, observation: func() Observation { o := greenObservation(); o.MergeStateStatus = "HAS_HOOKS"; return o }(), wantState: StateReady, wantReason: "green"},
		{name: "codex settles", mode: ModeCodex, observation: func() Observation { o := greenObservation(); o.CodexThumbsUp = true; return o }(), wantState: StateWaiting, wantReason: "codex_settling"},
		{name: "codex thumbs up", mode: ModeCodex, observation: func() Observation { o := greenObservation(); o.CodexThumbsUp = true; return o }(), cursor: Cursor{HeadSHA: "head-a", HeadObservedAt: now.Add(-CodexSettle)}, wantState: StateReady, wantReason: "codex_approved_best_effort"},
		{name: "codex eyes win", mode: ModeCodex, observation: func() Observation { o := greenObservation(); o.CodexThumbsUp, o.CodexEyes = true, true; return o }(), cursor: Cursor{HeadSHA: "head-a", HeadObservedAt: now.Add(-CodexSettle)}, wantState: StateWaiting, wantReason: "codex_reviewing"},
		{name: "aggregate approval", mode: ModeFormalReview, observation: func() Observation { o := greenObservation(); o.ReviewDecision = "APPROVED"; return o }(), wantState: StateReady, wantReason: "formal_review_approved"},
		{name: "selected approval", mode: ModeFormalReview, reviewer: "octo", observation: func() Observation {
			o := greenObservation()
			o.ReviewOpinions = []ReviewOpinion{{Actor: "octo", State: "APPROVED"}}
			return o
		}(), wantState: StateReady, wantReason: "selected_reviewer_approved"},
		{name: "selected bot approval", mode: ModeFormalReview, reviewer: "dependabot", observation: func() Observation {
			o := greenObservation()
			o.ReviewOpinions = []ReviewOpinion{{Actor: "dependabot[bot]", State: "APPROVED"}}
			return o
		}(), wantState: StateReady, wantReason: "selected_reviewer_approved"},
		{name: "selected reviewer requested again", mode: ModeFormalReview, reviewer: "octo", observation: func() Observation {
			o := greenObservation()
			o.ReviewOpinions = []ReviewOpinion{{Actor: "octo", State: "APPROVED"}}
			o.RequestedReviewers = []string{"octo"}
			return o
		}(), wantState: StateWaiting, wantReason: "selected_reviewer_requested"},
		{name: "changes requested", mode: ModeFormalReview, observation: func() Observation { o := greenObservation(); o.ReviewDecision = "CHANGES_REQUESTED"; return o }(), wantState: StateWaiting, wantReason: "changes_requested"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := Evaluate(test.observation, test.mode, test.reviewer, test.cursor, now)
			if got.State != test.wantState || got.Reason != test.wantReason {
				t.Fatalf("evaluation = %+v, want state=%s reason=%s", got, test.wantState, test.wantReason)
			}
		})
	}
}

func TestEveryMergeStateFailsClosedExceptCleanAndHooks(t *testing.T) {
	now := time.Now()
	for _, state := range []string{"BLOCKED", "BEHIND", "DIRTY", "UNSTABLE", "DRAFT", "UNKNOWN", "", "FUTURE"} {
		t.Run(state, func(t *testing.T) {
			o := greenObservation()
			o.MergeStateStatus = state
			got, _ := Evaluate(o, ModeGreen, "", Cursor{}, now)
			if got.State == StateReady {
				t.Fatalf("%q evaluated ready: %+v", state, got)
			}
		})
	}
}

func TestCodexDeadlineSurvivesRestartAndResetsOnHead(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	o := greenObservation()
	o.CodexThumbsUp = true
	first, cursor := Evaluate(o, ModeCodex, "", Cursor{}, now)
	if first.SettlingUntil == nil || !first.SettlingUntil.Equal(now.Add(CodexSettle)) {
		t.Fatalf("first = %+v", first)
	}
	restarted, cursor := Evaluate(o, ModeCodex, "", cursor, now.Add(CodexSettle))
	if restarted.State != StateReady {
		t.Fatalf("after restart = %+v", restarted)
	}
	o.HeadSHA = "head-b"
	changed, cursor := Evaluate(o, ModeCodex, "", cursor, now.Add(CodexSettle+time.Second))
	if changed.Reason != "codex_settling" || !cursor.HeadObservedAt.Equal(now.Add(CodexSettle+time.Second)) {
		t.Fatalf("new head = %+v cursor=%+v", changed, cursor)
	}
}

func TestAdvanceBaselinesFeedbackThenDeliversNewItemsAndReopenedThreads(t *testing.T) {
	now := time.Now()
	o := greenObservation()
	o.Feedback = []FeedbackItem{{ID: "old", Kind: "comment", Author: "a"}}
	o.Threads = []ThreadState{{ID: "thread", Resolved: true}}
	first := Advance(Cursor{}, o, ModeGreen, "", now)
	if len(first.Actions) != 1 || first.Actions[0].Kind != "ready" {
		t.Fatalf("first actions = %+v", first.Actions)
	}
	o.Feedback = append(o.Feedback, FeedbackItem{ID: "new", Kind: "reply", Author: "b"})
	o.Threads[0].Resolved = false
	second := Advance(first.Cursor, o, ModeGreen, "", now.Add(time.Minute))
	if len(second.Actions) != 2 || second.Actions[0].Kind != "reply" || second.Actions[1].Kind != "thread_reopened" {
		t.Fatalf("second actions = %+v", second.Actions)
	}
	third := Advance(second.Cursor, o, ModeGreen, "", now.Add(2*time.Minute))
	if len(third.Actions) != 0 {
		t.Fatalf("replayed actions = %+v", third.Actions)
	}
}

func TestAdvanceRedeliversRecurringActionsAndThreadReopens(t *testing.T) {
	now := time.Now()
	ready := greenObservation()
	ready.Threads = []ThreadState{{ID: "thread", Resolved: true}}
	first := Advance(Cursor{}, ready, ModeGreen, "", now)
	if len(first.Actions) != 1 || first.Actions[0].Kind != "ready" {
		t.Fatalf("first actions = %+v", first.Actions)
	}

	blocked := ready
	blocked.MergeStateStatus = "BLOCKED"
	blocked.Threads[0].Resolved = false
	second := Advance(first.Cursor, blocked, ModeGreen, "", now.Add(time.Minute))
	if len(second.Actions) != 1 || second.Actions[0].Kind != "thread_reopened" {
		t.Fatalf("blocked actions = %+v", second.Actions)
	}

	resolved := blocked
	resolved.Threads[0].Resolved = true
	third := Advance(second.Cursor, resolved, ModeGreen, "", now.Add(2*time.Minute))
	fourth := Advance(third.Cursor, ready, ModeGreen, "", now.Add(3*time.Minute))
	if len(fourth.Actions) != 1 || fourth.Actions[0].Kind != "ready" || fourth.Actions[0].ID == first.Actions[0].ID {
		t.Fatalf("recurring ready actions = first:%+v fourth:%+v", first.Actions, fourth.Actions)
	}

	reopened := ready
	reopened.MergeStateStatus = "BLOCKED"
	reopened.Threads[0].Resolved = false
	fifth := Advance(fourth.Cursor, reopened, ModeGreen, "", now.Add(4*time.Minute))
	if len(fifth.Actions) != 1 || fifth.Actions[0].Kind != "thread_reopened" || fifth.Actions[0].ID == second.Actions[0].ID {
		t.Fatalf("recurring thread actions = second:%+v fifth:%+v", second.Actions, fifth.Actions)
	}
}

func TestAdvanceReevaluatesAReadyActionForANewHead(t *testing.T) {
	now := time.Now()
	first := Advance(Cursor{}, greenObservation(), ModeGreen, "", now)
	nextHead := greenObservation()
	nextHead.HeadSHA = "head-b"
	second := Advance(first.Cursor, nextHead, ModeGreen, "", now.Add(time.Minute))
	if len(second.Actions) != 1 || second.Actions[0].Kind != "ready" || second.Actions[0].ID == first.Actions[0].ID {
		t.Fatalf("head transition actions = first:%+v second:%+v", first.Actions, second.Actions)
	}
}

func TestAdvanceBaselinesFeedbackAfterInitialFetchFailure(t *testing.T) {
	now := time.Now()
	initial := greenObservation()
	initial.FeedbackComplete = false
	first := Advance(Cursor{}, initial, ModeGreen, "", now)
	if !first.Cursor.FeedbackBaselinePending {
		t.Fatalf("first cursor = %+v", first.Cursor)
	}

	recovered := greenObservation()
	recovered.Feedback = []FeedbackItem{{ID: "historical", Kind: "comment", Author: "a"}}
	second := Advance(first.Cursor, recovered, ModeGreen, "", now.Add(time.Minute))
	if len(second.Actions) != 0 || second.Cursor.FeedbackBaselinePending {
		t.Fatalf("recovery transition = %+v", second)
	}

	recovered.Feedback = append(recovered.Feedback, FeedbackItem{ID: "new", Kind: "comment", Author: "b"})
	third := Advance(second.Cursor, recovered, ModeGreen, "", now.Add(2*time.Minute))
	if len(third.Actions) != 1 || third.Actions[0].ID != "feedback:new" {
		t.Fatalf("new feedback actions = %+v", third.Actions)
	}
}

func TestAdvanceIncludesChangesRequestedReviewBody(t *testing.T) {
	observation := greenObservation()
	observation.ReviewDecision = "CHANGES_REQUESTED"
	observation.ReviewOpinions = []ReviewOpinion{{Actor: "victor", State: "CHANGES_REQUESTED", Body: "Please cover the restart path."}}
	transition := Advance(Cursor{}, observation, ModeFormalReview, "", time.Now())
	if len(transition.Actions) != 1 || transition.Actions[0].Kind != "changes_requested" {
		t.Fatalf("actions = %+v", transition.Actions)
	}
	want := "victor: Please cover the restart path."
	if len(transition.Actions[0].Details) != 1 || transition.Actions[0].Details[0] != want {
		t.Fatalf("details = %+v, want %q", transition.Actions[0].Details, want)
	}
}

func TestModeChangePreservesFeedbackButRestartsSettling(t *testing.T) {
	now := time.Now()
	cursor := Cursor{Initialized: true, HeadSHA: "head-a", HeadObservedAt: now.Add(-time.Hour), SeenFeedbackIDs: []string{"f"}, ThreadStates: map[string]bool{"t": true}, LastAction: "ready"}
	reset := PreserveFeedbackCursor(cursor)
	if !reset.Initialized || reset.HeadSHA != "" || len(reset.SeenFeedbackIDs) != 1 || !reset.ThreadStates["t"] {
		t.Fatalf("reset = %+v", reset)
	}
	evaluation, _ := Evaluate(greenObservation(), ModeCodex, "", reset, now)
	if evaluation.Reason != "codex_settling" {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestModeChangeDoesNotBaselineFeedbackThatArrivedBeforeTheNextPoll(t *testing.T) {
	now := time.Now()
	initial := greenObservation()
	initial.Feedback = []FeedbackItem{{ID: "old", Kind: "comment", Author: "a"}}
	first := Advance(Cursor{}, initial, ModeGreen, "", now)

	reconfigured := PreserveFeedbackCursor(first.Cursor)
	current := greenObservation()
	current.Feedback = append(initial.Feedback, FeedbackItem{ID: "new", Kind: "comment", Author: "b"})
	second := Advance(reconfigured, current, ModeFormalReview, "", now.Add(time.Minute))
	if len(second.Actions) != 1 || second.Actions[0].ID != "feedback:new" {
		t.Fatalf("actions after mode change = %+v", second.Actions)
	}
}

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(ModeGreen, "reviewer"); err == nil {
		t.Fatal("green accepted a reviewer")
	}
	if err := ValidateConfig(ModeFormalReview, ""); err != nil {
		t.Fatalf("optional reviewer rejected: %v", err)
	}
}
