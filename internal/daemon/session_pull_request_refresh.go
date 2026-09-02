package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	sessionPullRequestRefreshKind    = "session_pull_request_refresh"
	sessionPullRequestRefreshTimeout = 2 * time.Minute
)

const sessionPullRequestRefreshTick = protocol.HeatHotInterval

const (
	sessionPullRequestOpen   = "open"
	sessionPullRequestMerged = "merged"
	sessionPullRequestClosed = "closed"
)

// The registry hands out a concrete *github.Client; this narrow view is the test seam.
type sessionPRHost interface {
	FetchPullRequestSnapshot(repo string, number int) (*github.PullRequestSnapshot, error)
	FetchPullRequestReviewStatus(repo string, number int) (string, error)
	IsRateLimited(resource string) (bool, time.Time)
	GetRateLimit(resource string) *github.RateLimitInfo
}

func (d *Daemon) sessionPRHostFor(host string) (sessionPRHost, bool) {
	if d.sessionPRHosts != nil {
		return d.sessionPRHosts(host)
	}
	if d.ghRegistry == nil {
		return nil, false
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, false
	}
	return client, true
}

func (d *Daemon) registerSessionPullRequestRefreshCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		sessionPullRequestRefreshKind,
		sessionPullRequestRefreshTick,
		d.sessionPullRequestRefreshHandler,
		jobs.HandlerConfig{Timeout: sessionPullRequestRefreshTimeout},
	); err != nil {
		d.logf("session pull requests: register refresh tick: %v", err)
	}
}

func (d *Daemon) sessionPullRequestRefreshHandler(_ context.Context, _ *jobs.Job) (any, error) {
	fetched, changed := d.refreshSessionPullRequests(time.Now())
	return map[string]any{"fetched": fetched, "changed": changed}, nil
}

type sessionPullRequestGroup struct {
	prID     string
	host     string
	repo     string
	number   int
	previous store.SessionPullRequestStatus
	sessions []string
	due      bool
}

func (d *Daemon) refreshSessionPullRequests(now time.Time) (fetched, changed int) {
	if d.store == nil {
		return 0, 0
	}
	groups := d.dueSessionPullRequests(d.store.OpenSessionPullRequests(), now)
	if len(groups) == 0 {
		return 0, 0
	}

	limitedHosts := make(map[string]time.Time)
	var changedSessions []string
	for _, group := range groups {
		if _, limited := limitedHosts[group.host]; limited {
			continue
		}
		host, ok := d.sessionPRHostFor(group.host)
		if !ok {
			d.logf("session pull requests: no GitHub client for host %s, %s stays as recorded", group.host, group.prID)
			d.markSessionPullRequestChecked(group.prID, now)
			continue
		}
		if limited, resetAt := host.IsRateLimited("core"); limited {
			d.logf("session pull requests: %s rate limited until %s", group.host, resetAt.Format(time.RFC3339))
			limitedHosts[group.host] = resetAt
			continue
		}

		status, err := d.fetchSessionPullRequestStatus(host, group)
		if err != nil {
			if resetAt, limited := hostRateLimitReset(host, err); limited {
				d.logf("session pull requests: %s rate limited mid-refresh, stopping there: %v", group.host, err)
				limitedHosts[group.host] = resetAt
				continue
			}
			d.logf("session pull requests: refresh %s: %v", group.prID, err)
			d.markSessionPullRequestChecked(group.prID, now)
			continue
		}

		fetched++
		if err := d.store.UpdateSessionPullRequestStatus(group.prID, status, now); err != nil {
			d.logf("session pull requests: store status for %s: %v", group.prID, err)
			continue
		}
		if status == group.previous {
			continue
		}
		changed++
		if err := d.store.TouchSessionPullRequestActivity(group.prID, now); err != nil {
			d.logf("session pull requests: mark %s active: %v", group.prID, err)
		}
		changedSessions = append(changedSessions, group.sessions...)
	}

	d.broadcastSessionPullRequestLimits(limitedHosts)
	if len(changedSessions) > 0 {
		d.coalesceSnapshots(func() {
			for _, sessionID := range changedSessions {
				d.publishFact(FactSessionPullRequestChanged, sessionID, nil)
			}
		})
	}
	if changed > 0 {
		d.settleHarvestConditions()
	}
	return fetched, changed
}

func (d *Daemon) dueSessionPullRequests(records []store.SessionPullRequestRecord, now time.Time) []*sessionPullRequestGroup {
	var groups []*sessionPullRequestGroup
	byPR := make(map[string]*sessionPullRequestGroup)
	for _, rec := range records {
		if !d.sessionPullRequestSessionActive(rec.SessionID) {
			continue
		}
		group := byPR[rec.PRID]
		if group == nil {
			host, repo, ok := splitPullRequestRepository(rec.Repository)
			if !ok {
				d.logf("session pull requests: %s has no usable repository %q", rec.PRID, rec.Repository)
				continue
			}
			group = &sessionPullRequestGroup{
				prID: rec.PRID, host: host, repo: repo, number: rec.Number,
				previous: sessionPullRequestStatusOf(rec),
			}
			byPR[rec.PRID] = group
			groups = append(groups, group)
		}
		group.sessions = append(group.sessions, rec.SessionID)
		group.due = group.due || sessionPullRequestDue(rec, now)
	}

	var due []*sessionPullRequestGroup
	for _, group := range groups {
		if group.due {
			due = append(due, group)
		}
	}
	return due
}

func (d *Daemon) sessionPullRequestSessionActive(sessionID string) bool {
	session := d.store.Get(sessionID)
	return session != nil && session.State != protocol.SessionStateRecoverable
}

func (d *Daemon) fetchSessionPullRequestStatus(host sessionPRHost, group *sessionPullRequestGroup) (store.SessionPullRequestStatus, error) {
	snapshot, err := host.FetchPullRequestSnapshot(group.repo, group.number)
	if err != nil {
		return store.SessionPullRequestStatus{}, err
	}

	// A closed pull request reports mergeable_state "unknown", which would erase a real result.
	status := group.previous
	status.Title = snapshot.Title
	status.Draft = snapshot.Draft
	status.State = sessionPullRequestStateFromSnapshot(snapshot)
	status.HeadSHA = snapshot.HeadSHA
	status.HeadBranch = snapshot.HeadRef
	if status.State != sessionPullRequestOpen {
		return status, nil
	}

	status.MergeableState = snapshot.MergeableState
	status.CIStatus = github.CIStatusFromMergeableState(snapshot.MergeableState)
	review, err := host.FetchPullRequestReviewStatus(group.repo, group.number)
	if err != nil {
		d.logf("session pull requests: reviews for %s: %v", group.prID, err)
		return status, nil
	}
	status.ReviewStatus = review
	return status, nil
}

func (d *Daemon) markSessionPullRequestChecked(prID string, now time.Time) {
	if err := d.store.MarkSessionPullRequestChecked(prID, now); err != nil {
		d.logf("session pull requests: move the pacing cursor for %s: %v", prID, err)
	}
}

func (d *Daemon) broadcastSessionPullRequestLimits(limitedHosts map[string]time.Time) {
	var earliest time.Time
	for _, resetAt := range limitedHosts {
		if resetAt.IsZero() {
			continue
		}
		if earliest.IsZero() || resetAt.Before(earliest) {
			earliest = resetAt
		}
	}
	if !earliest.IsZero() {
		d.broadcastRateLimited("core", earliest)
	}
}

func hostRateLimitReset(host sessionPRHost, err error) (time.Time, bool) {
	if errors.Is(err, github.ErrSelfRateLimited) {
		return time.Now().Add(time.Minute), true
	}
	if !errors.Is(err, github.ErrRateLimited) {
		return time.Time{}, false
	}
	if info := host.GetRateLimit("core"); info != nil {
		return info.ResetAt, true
	}
	return time.Now().Add(time.Minute), true
}

func sessionPullRequestStateFromSnapshot(snapshot *github.PullRequestSnapshot) string {
	if snapshot.State == sessionPullRequestOpen {
		return sessionPullRequestOpen
	}
	if snapshot.Merged {
		return sessionPullRequestMerged
	}
	return sessionPullRequestClosed
}

func sessionPullRequestStatusOf(rec store.SessionPullRequestRecord) store.SessionPullRequestStatus {
	return store.SessionPullRequestStatus{
		Title: rec.Title, Draft: rec.Draft, State: rec.State,
		CIStatus: rec.CIStatus, ReviewStatus: rec.ReviewStatus,
		MergeableState: rec.MergeableState, HeadSHA: rec.HeadSHA, HeadBranch: rec.HeadBranch,
	}
}

func splitPullRequestRepository(repository string) (host, repo string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(repository), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func sessionPullRequestRefreshInterval(rec store.SessionPullRequestRecord, now time.Time) time.Duration {
	age := now.Sub(protocol.Timestamp(rec.LastActivityAt).Time())
	switch {
	case age < protocol.HeatHotDuration:
		return protocol.HeatHotInterval
	case age < protocol.HeatWarmDuration:
		return protocol.HeatWarmInterval
	default:
		return protocol.HeatColdInterval
	}
}

func sessionPullRequestDue(rec store.SessionPullRequestRecord, now time.Time) bool {
	checked := protocol.Timestamp(rec.StatusCheckedAt).Time()
	if checked.IsZero() {
		return true
	}
	return now.Sub(checked) >= sessionPullRequestRefreshInterval(rec, now)
}

func (d *Daemon) subscribeSessionPullRequestFacts() {
	if d.eventBus == nil || d.sessionPRUnsubHooks != nil {
		return
	}
	d.sessionPRUnsubHooks = d.eventBus.Subscribe(
		bus.Filter{FactPRUpdated, FactPRDisappeared},
		d.reheatSessionPullRequest,
	)
}

func (d *Daemon) unsubscribeSessionPullRequestFacts() {
	if d.sessionPRUnsubHooks != nil {
		d.sessionPRUnsubHooks()
		d.sessionPRUnsubHooks = nil
	}
}

// Runs inside the bus fan-out: no nested publish.
func (d *Daemon) reheatSessionPullRequest(event bus.Event) {
	if d.store == nil {
		return
	}
	if err := d.store.TouchSessionPullRequestActivity(event.Subject, time.Now()); err != nil {
		d.logf("session pull requests: reheat %s: %v", event.Subject, err)
	}
}
