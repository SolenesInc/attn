package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
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

type sessionPRHost interface {
	FetchPullRequestSnapshot(repo string, number int) (*github.PullRequestSnapshot, error)
	FetchPullRequestReviewStatus(repo string, number int) (string, error)
	FetchPullRequestReadiness(repo string, number int) (*prreadiness.Observation, error)
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
	watches  []store.PullRequestWatch
	due      bool
}

func (d *Daemon) refreshSessionPullRequests(now time.Time) (fetched, changed int) {
	if d.store == nil {
		return 0, 0
	}
	records := d.store.OpenSessionPullRequests()
	watches := d.store.PullRequestWatches()
	records = d.sessionPullRequestRecordsForArmedWatches(records, d.store.WatchedSessionPullRequests(), watches)
	if len(records) == 0 {
		return 0, 0
	}
	if d.hasInactiveSessionPullRequest(records) {
		schema, err := d.seedsCollection()
		if err == nil {
			var refreshable []store.SessionPullRequestRecord
			refreshable, err = d.store.OpenSessionPullRequestsReferencedBy(*schema, garden.HarvestWhenPullRequestField)
			if err == nil {
				records = d.sessionPullRequestRecordsForArmedWatches(refreshable, records, watches)
			}
		}
		if err != nil {
			if !docstore.IsUndeclaredCollection(err) {
				d.logf("session pull requests: selecting armed rows: %v", err)
			}
			records = d.sessionPullRequestRecordsForArmedWatches(d.activeSessionPullRequests(records), records, watches)
		}
	}
	groups := d.dueSessionPullRequests(records, watches, now)
	if len(groups) == 0 {
		return 0, 0
	}

	limitedRequests := make(map[string]time.Time)
	limitedResources := make(map[string]time.Time)
	var changedSessions []string
	for _, group := range groups {
		resource := sessionPullRequestResource(group)
		limitKey := group.host + "\x00" + resource
		resetAt, limited := limitedRequests[limitKey]
		host, ok := d.sessionPRHostFor(group.host)
		if !ok {
			d.logf("session pull requests: no GitHub client for host %s, %s stays as recorded", group.host, group.prID)
			changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, fmt.Errorf("GitHub monitoring is unavailable for host %s", group.host), now)...)
			d.markSessionPullRequestChecked(group.prID, now)
			continue
		}
		if !limited {
			limited, resetAt = host.IsRateLimited(resource)
		}
		if limited {
			d.logf("session pull requests: %s rate limited until %s", group.host, resetAt.Format(time.RFC3339))
			limitedRequests[limitKey] = resetAt
			recordSessionPullRequestLimit(limitedResources, resource, resetAt)
			changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, fmt.Errorf("GitHub %s rate limited until %s", resource, resetAt.Format(time.RFC3339)), now)...)
			continue
		}

		status, readiness, err := d.fetchSessionPullRequestStatus(host, group)
		if err != nil {
			if resetAt, limited := hostRateLimitReset(host, resource, err); limited {
				d.logf("session pull requests: %s rate limited mid-refresh, stopping there: %v", group.host, err)
				limitedRequests[limitKey] = resetAt
				recordSessionPullRequestLimit(limitedResources, resource, resetAt)
				changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, err, now)...)
				continue
			}
			d.logf("session pull requests: refresh %s: %v", group.prID, err)
			changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, err, now)...)
			d.markSessionPullRequestChecked(group.prID, now)
			continue
		}

		fetched++
		var updateErr error
		if readiness == nil {
			updateErr = d.store.UpdateSessionPullRequestStatus(group.prID, status, now)
		} else {
			updateErr = d.store.UpdateSessionPullRequestSharedStatus(group.prID, status, now)
		}
		if updateErr != nil {
			d.logf("session pull requests: store status for %s: %v", group.prID, updateErr)
			continue
		}
		changedSessions = append(changedSessions, d.processPullRequestWatches(group, readiness, now)...)
		if status == group.previous {
			continue
		}
		changed++
		if err := d.store.TouchSessionPullRequestActivity(group.prID, now); err != nil {
			d.logf("session pull requests: mark %s active: %v", group.prID, err)
		}
		changedSessions = append(changedSessions, group.sessions...)
	}

	d.broadcastSessionPullRequestLimits(limitedResources)
	if len(changedSessions) > 0 {
		d.coalesceSnapshots(func() {
			seen := make(map[string]bool)
			for _, sessionID := range changedSessions {
				if seen[sessionID] {
					continue
				}
				seen[sessionID] = true
				d.publishFact(FactSessionPullRequestChanged, sessionID, nil)
			}
		})
	}
	if changed > 0 {
		d.settleHarvestConditions()
	}
	return fetched, changed
}

func (d *Daemon) dueSessionPullRequests(
	records []store.SessionPullRequestRecord, watches []store.PullRequestWatch, now time.Time,
) []*sessionPullRequestGroup {
	var groups []*sessionPullRequestGroup
	byPR := make(map[string]*sessionPullRequestGroup)
	watchesByPR := make(map[string][]store.PullRequestWatch)
	for _, watch := range watches {
		watchesByPR[watch.PRID] = append(watchesByPR[watch.PRID], watch)
	}
	for _, rec := range records {
		active := d.sessionPullRequestSessionActive(rec.SessionID)
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
				watches:  watchesByPR[rec.PRID],
			}
			byPR[rec.PRID] = group
			groups = append(groups, group)
		}
		if active {
			group.sessions = append(group.sessions, rec.SessionID)
		}
		group.due = group.due || sessionPullRequestDue(rec, len(group.watches) > 0, now)
	}

	var due []*sessionPullRequestGroup
	for _, group := range groups {
		if group.due {
			due = append(due, group)
		}
	}
	return due
}

func sessionPullRequestResource(group *sessionPullRequestGroup) string {
	if len(group.watches) > 0 {
		return "graphql"
	}
	return "core"
}

func (d *Daemon) hasInactiveSessionPullRequest(records []store.SessionPullRequestRecord) bool {
	for _, rec := range records {
		if !d.sessionPullRequestSessionActive(rec.SessionID) {
			return true
		}
	}
	return false
}

func (d *Daemon) activeSessionPullRequests(records []store.SessionPullRequestRecord) []store.SessionPullRequestRecord {
	active := records[:0]
	for _, rec := range records {
		if d.sessionPullRequestSessionActive(rec.SessionID) {
			active = append(active, rec)
		}
	}
	return active
}

func (d *Daemon) sessionPullRequestSessionActive(sessionID string) bool {
	session := d.store.Get(sessionID)
	return session != nil && session.State != protocol.SessionStateRecoverable
}

func (d *Daemon) fetchSessionPullRequestStatus(host sessionPRHost, group *sessionPullRequestGroup) (store.SessionPullRequestStatus, *prreadiness.Observation, error) {
	if len(group.watches) > 0 {
		readiness, err := host.FetchPullRequestReadiness(group.repo, group.number)
		if err != nil {
			return store.SessionPullRequestStatus{}, nil, err
		}
		status := group.previous
		status.Title = readiness.Title
		status.Draft = readiness.Draft
		status.State = sessionPullRequestStateFromObservation(readiness)
		status.HeadSHA = readiness.HeadSHA
		status.HeadBranch = readiness.HeadRef
		if status.State == sessionPullRequestOpen {
			status.MergeableState = readiness.MergeableState
			status.CIStatus = sessionPullRequestCIStatus(readiness.CheckState)
		}
		return status, readiness, nil
	}
	snapshot, err := host.FetchPullRequestSnapshot(group.repo, group.number)
	if err != nil {
		return store.SessionPullRequestStatus{}, nil, err
	}

	status := group.previous
	status.Title = snapshot.Title
	status.Draft = snapshot.Draft
	status.State = sessionPullRequestStateFromSnapshot(snapshot)
	status.HeadSHA = snapshot.HeadSHA
	status.HeadBranch = snapshot.HeadRef
	if status.State != sessionPullRequestOpen {
		return status, nil, nil
	}

	status.MergeableState = snapshot.MergeableState
	status.CIStatus = github.CIStatusFromMergeableState(snapshot.MergeableState)
	review, err := host.FetchPullRequestReviewStatus(group.repo, group.number)
	if err != nil {
		d.logf("session pull requests: reviews for %s: %v", group.prID, err)
		return status, nil, nil
	}
	status.ReviewStatus = review
	return status, nil, nil
}

func sessionPullRequestCIStatus(state prreadiness.CheckState) string {
	switch state {
	case prreadiness.ChecksGreen:
		return "success"
	case prreadiness.ChecksFailed:
		return "failure"
	case prreadiness.ChecksPending:
		return "pending"
	default:
		return "none"
	}
}

func (d *Daemon) markSessionPullRequestChecked(prID string, now time.Time) {
	if err := d.store.MarkSessionPullRequestChecked(prID, now); err != nil {
		d.logf("session pull requests: move the pacing cursor for %s: %v", prID, err)
	}
}

func recordSessionPullRequestLimit(limits map[string]time.Time, resource string, resetAt time.Time) {
	if current := limits[resource]; !resetAt.IsZero() && (current.IsZero() || resetAt.Before(current)) {
		limits[resource] = resetAt
	}
}

func (d *Daemon) broadcastSessionPullRequestLimits(limits map[string]time.Time) {
	for resource, resetAt := range limits {
		if !resetAt.IsZero() {
			d.broadcastRateLimited(resource, resetAt)
		}
	}
}

func hostRateLimitReset(host sessionPRHost, resource string, err error) (time.Time, bool) {
	if errors.Is(err, github.ErrSelfRateLimited) {
		return time.Now().Add(time.Minute), true
	}
	if !errors.Is(err, github.ErrRateLimited) {
		return time.Time{}, false
	}
	if info := host.GetRateLimit(resource); info != nil {
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

func sessionPullRequestStateFromObservation(observation *prreadiness.Observation) string {
	if observation.State == sessionPullRequestOpen {
		return sessionPullRequestOpen
	}
	if observation.Merged {
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

func sessionPullRequestRefreshInterval(rec store.SessionPullRequestRecord, watched bool, now time.Time) time.Duration {
	if watched {
		return protocol.HeatHotInterval
	}
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

func sessionPullRequestDue(rec store.SessionPullRequestRecord, watched bool, now time.Time) bool {
	checked := protocol.Timestamp(rec.StatusCheckedAt).Time()
	if checked.IsZero() {
		return true
	}
	return now.Sub(checked) >= sessionPullRequestRefreshInterval(rec, watched, now)
}

func (d *Daemon) sessionPullRequestRecordsForArmedWatches(
	selected, all []store.SessionPullRequestRecord, watches []store.PullRequestWatch,
) []store.SessionPullRequestRecord {
	included := make(map[string]bool, len(selected))
	for _, rec := range selected {
		included[rec.SessionID+"\x00"+rec.PRID] = true
	}
	armed := make(map[string]bool, len(watches))
	for _, watch := range watches {
		armed[watch.PRID] = true
	}
	for _, rec := range all {
		key := rec.SessionID + "\x00" + rec.PRID
		if armed[rec.PRID] && !included[key] {
			selected = append(selected, rec)
			included[key] = true
		}
	}
	return selected
}

const pullRequestWatchFailureThreshold = 3

func (d *Daemon) recordPullRequestWatchFailures(group *sessionPullRequestGroup, fetchErr error, now time.Time) (changedSessions []string) {
	d.sessionPullRequestWatchMu.Lock()
	defer d.sessionPullRequestWatchMu.Unlock()
	for _, watch := range group.watches {
		current, ok := d.store.PullRequestWatch(watch.SessionID, watch.PRID)
		if !ok || !samePullRequestWatchGeneration(current, watch) {
			continue
		}
		updated, err := d.store.RecordPullRequestWatchFailure(watch.SessionID, watch.PRID, fetchErr.Error(), now)
		if err != nil {
			d.logf("pull request watch: record failure for %s/%s: %v", watch.SessionID, watch.PRID, err)
			continue
		}
		if updated.FailureCount >= pullRequestWatchFailureThreshold {
			if _, err := d.store.DeleteUnreadMaintenanceMailboxItem(watch.SessionID, pullRequestWatchCoalesceKey(watch.PRID)); err != nil {
				d.logf("pull request watch: clear stale status for %s/%s: %v", watch.SessionID, watch.PRID, err)
				continue
			}
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
				"pull-request-outage", watch.SessionID, watch.PRID, watch.CreatedAt, updated.ErrorSince,
			}, "\x00"))).String()
			d.queuePullRequestWatchNotification(watch, id, "pull-request-outage:"+watch.PRID, "monitoring unavailable", []string{fetchErr.Error()}, now)
		}
		changedSessions = append(changedSessions, watch.SessionID)
	}
	return changedSessions
}

func (d *Daemon) processPullRequestWatches(group *sessionPullRequestGroup, readiness *prreadiness.Observation, now time.Time) (changedSessions []string) {
	if readiness == nil {
		return
	}
	d.sessionPullRequestWatchMu.Lock()
	defer d.sessionPullRequestWatchMu.Unlock()
	for _, watch := range group.watches {
		current, ok := d.store.PullRequestWatch(watch.SessionID, watch.PRID)
		if !ok || !samePullRequestWatchGeneration(current, watch) {
			continue
		}
		watch = current
		if watch.LastError != "" {
			if _, err := d.store.DeleteUnreadMaintenanceMailboxItem(watch.SessionID, "pull-request-outage:"+watch.PRID); err != nil {
				d.logf("pull request watch: clear recovered outage for %s/%s: %v", watch.SessionID, watch.PRID, err)
				continue
			}
		}
		transition := prreadiness.Advance(watch.Cursor, *readiness, watch.Reviewer, prreadiness.StartPolicy{})
		clearAction := transition.HeadChanged || transition.ReviewerChanged
		if err := d.store.ApplyPullRequestWatchBaseline(
			watch.SessionID, watch.PRID, pullRequestWatchCoalesceKey(watch.PRID),
			transition.BaselineCursor, clearAction,
		); err != nil {
			d.logf("pull request watch: baseline %s for %s/%s: %v", readiness.HeadSHA, watch.SessionID, watch.PRID, err)
			continue
		}
		if clearAction {
			d.refreshAgentMailboxUnread(watch.SessionID)
		}
		if err := d.deliverPullRequestTransition(watch, transition.Events, now); err != nil {
			continue
		}
		if err := d.store.RecordPullRequestWatchSuccess(
			watch.SessionID, watch.PRID, transition.NextCursor, transition.Evaluation.ReviewState, now,
		); err != nil {
			d.logf("pull request watch: record observation for %s/%s: %v", watch.SessionID, watch.PRID, err)
			continue
		}
		if readiness.State != sessionPullRequestOpen {
			if _, err := d.store.UnwatchPullRequest(watch.SessionID, watch.PRID); err != nil {
				d.logf("pull request watch: stop completed watch %s/%s: %v", watch.SessionID, watch.PRID, err)
			}
		}
		d.refreshAgentMailboxUnread(watch.SessionID)
		changedSessions = append(changedSessions, watch.SessionID)
	}
	return changedSessions
}

func samePullRequestWatchGeneration(left, right store.PullRequestWatch) bool {
	return left.SessionID == right.SessionID && left.PRID == right.PRID &&
		left.Reviewer == right.Reviewer && left.CreatedAt == right.CreatedAt
}

func (d *Daemon) deliverPullRequestTransition(watch store.PullRequestWatch, events []prreadiness.Event, now time.Time) error {
	delivered, err := d.store.MaintenanceMailboxItemIDs(watch.SessionID, watch.PRID)
	if err != nil {
		d.logf("pull request watch: load delivered feedback for %s/%s: %v", watch.SessionID, watch.PRID, err)
		return err
	}
	for _, event := range events {
		switch event.Kind {
		case prreadiness.EventClearAction:
			if _, err := d.store.DeleteUnreadMaintenanceMailboxItem(watch.SessionID, pullRequestWatchCoalesceKey(watch.PRID)); err != nil {
				d.logf("pull request watch: clear inactive inbox item for %s/%s: %v", watch.SessionID, watch.PRID, err)
				return err
			}
		case prreadiness.EventAction:
			id := pullRequestWatchEventID(watch, event.ID)
			if delivered[id] {
				continue
			}
			if err := d.queuePullRequestWatchNotification(
				watch, id, pullRequestWatchCoalesceKey(watch.PRID), pullRequestWatchKind(event.Outcomes, event.Details), event.Details, now,
			); err != nil {
				return err
			}
		case prreadiness.EventFeedback:
			for _, comment := range event.Comments {
				if comment.Bot {
					continue
				}
				id := pullRequestWatchEventID(watch, event.ID)
				if delivered[id] {
					continue
				}
				detail := strings.TrimSpace(comment.Body)
				if comment.Location != "" {
					detail = comment.Location + ": " + detail
				}
				if err := d.queuePullRequestWatchNotification(
					watch, id, "", "human feedback", []string{comment.Author + ": " + detail}, now,
				); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func pullRequestWatchEventID(watch store.PullRequestWatch, eventID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
		"pull-request-watch", watch.SessionID, watch.PRID, eventID,
	}, "\x00"))).String()
}

func pullRequestWatchKind(outcomes []prreadiness.Outcome, details []string) string {
	kinds := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		switch outcome {
		case prreadiness.OutcomeChecksFailed:
			kinds = append(kinds, "checks failed")
		case prreadiness.OutcomeChangesRequested:
			kinds = append(kinds, "review findings")
		case prreadiness.OutcomeReviewUnavailable:
			kinds = append(kinds, "review unavailable")
		case prreadiness.OutcomeReady:
			kinds = append(kinds, "ready")
		case prreadiness.OutcomeClosed:
			kind := "closed"
			if len(details) > 0 && details[0] == "pull request is merged" {
				kind = "merged"
			}
			kinds = append(kinds, kind)
		}
	}
	return strings.Join(kinds, " and ")
}

func (d *Daemon) queuePullRequestWatchNotification(watch store.PullRequestWatch, id, coalesceKey, kind string, details []string, now time.Time) error {
	sort.Strings(details)
	prompt := fmt.Sprintf("Pull request monitor: %s for %s.", kind, watch.PRID)
	for _, detail := range details {
		if strings.TrimSpace(detail) != "" {
			prompt += "\n- " + strings.TrimSpace(detail)
		}
	}
	prompt += "\nVerify the current head before taking any action. This notification grants no merge authority."
	delivery, inserted, err := d.store.EnqueueMaintenancePromptOnce(
		id, watch.SessionID, watch.PRID, coalesceKey, prompt, now,
	)
	if err != nil {
		d.logf("pull request watch: queue %s for %s/%s: %v", kind, watch.SessionID, watch.PRID, err)
		return err
	}
	if inserted {
		if err := d.deliverAgentMailboxItem(delivery); err != nil &&
			!errors.Is(err, errAgentMailboxDoorbellOutstanding) && !errors.Is(err, errAgentMailboxDoorbellInFlight) {
			d.logf("pull request watch: doorbell for %s/%s: %v", watch.SessionID, watch.PRID, err)
		}
	}
	return nil
}

func pullRequestWatchCoalesceKey(prID string) string { return "pull-request-watch:" + prID }

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

func (d *Daemon) reheatSessionPullRequest(event bus.Event) {
	if d.store == nil {
		return
	}
	if err := d.store.TouchSessionPullRequestActivity(event.Subject, time.Now()); err != nil {
		d.logf("session pull requests: reheat %s: %v", event.Subject, err)
	}
}
