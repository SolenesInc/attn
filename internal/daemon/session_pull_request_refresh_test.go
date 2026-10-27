package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
)

type fakePRHost struct {
	snapshot       *github.PullRequestSnapshot
	review         string
	err            error
	reviewErr      error
	limited        bool
	limitReset     time.Time
	snapshots      int
	reviews        int
	readiness      *prreadiness.Observation
	feedback       []prreadiness.FeedbackItem
	threads        []prreadiness.ThreadState
	readyErr       error
	feedbackErr    error
	readinessCalls int
	feedbackCalls  int
}

func (f *fakePRHost) FetchPullRequestReadiness(context.Context, string, int) (*prreadiness.Observation, error) {
	f.readinessCalls++
	if f.readyErr != nil {
		return nil, f.readyErr
	}
	copy := *f.readiness
	return &copy, nil
}

func (f *fakePRHost) FetchPullRequestFeedback(context.Context, string, int) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	f.feedbackCalls++
	return append([]prreadiness.FeedbackItem(nil), f.feedback...), append([]prreadiness.ThreadState(nil), f.threads...), f.feedbackErr
}

func (f *fakePRHost) FetchPullRequestSnapshot(string, int) (*github.PullRequestSnapshot, error) {
	f.snapshots++
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}

func (f *fakePRHost) FetchPullRequestReviewStatus(string, int) (string, error) {
	f.reviews++
	if f.reviewErr != nil {
		return "", f.reviewErr
	}
	return f.review, nil
}

func (f *fakePRHost) IsRateLimited(string) (bool, time.Time) { return f.limited, f.limitReset }

func (f *fakePRHost) GetRateLimit(string) *github.RateLimitInfo {
	if f.limitReset.IsZero() {
		return nil
	}
	return &github.RateLimitInfo{Resource: "core", ResetAt: f.limitReset}
}

func serveHost(d *Daemon, host string, served *fakePRHost) {
	d.sessionPRHosts = func(name string) (sessionPRHost, bool) {
		if name != host {
			return nil, false
		}
		return served, true
	}
}

func TestSessionPullRequestRefreshKeepsWatchingRecoverableSessions(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	host := &fakePRHost{readiness: readinessObservation()}
	serveHost(d, "github.com", host)
	if !d.store.SaveSessionRuntime("s1", store.SessionRuntimeState{State: protocol.SessionStateRecoverable, Since: time.Now(), Updated: time.Now()}) {
		t.Fatal("could not put the session in the recoverable state")
	}

	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 1 {
		t.Fatalf("fetched = %d, want the durable watch to remain active", fetched)
	}
	if host.readinessCalls != 1 {
		t.Fatalf("readiness calls = %d, want one", host.readinessCalls)
	}
}

func readinessObservation() *prreadiness.Observation {
	return &prreadiness.Observation{
		Number: 71, URL: "https://github.com/victorarias/attn/pull/71", Title: "Ready to ship",
		State: "open", HeadSHA: "head-a", HeadRef: "pr-readiness", MergeStateStatus: "CLEAN",
	}
}

func watchPRForRefresh(t *testing.T, d *Daemon, sessionID string, mode prreadiness.Mode, reviewer string) {
	t.Helper()
	rec, err := d.sessionPullRequestIdentity(protocol.SessionID(sessionID), "https://github.com/victorarias/attn/pull/71")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.watchSessionPullRequest(rec, mode, reviewer); err != nil {
		t.Fatal(err)
	}
}
