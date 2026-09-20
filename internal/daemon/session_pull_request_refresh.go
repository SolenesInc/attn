package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
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
	sessionPullRequestSettleKind     = "session_pull_request_settle"
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
	IsRateLimited(resource string) (bool, time.Time)
	GetRateLimit(resource string) *github.RateLimitInfo
}

type sessionPRReadinessHost interface {
	FetchPullRequestReadiness(context.Context, string, int) (*prreadiness.Observation, error)
	FetchPullRequestFeedback(context.Context, string, int) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error)
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
	if err := runner.RegisterWith(
		sessionPullRequestSettleKind,
		d.sessionPullRequestSettleHandler,
		jobs.HandlerConfig{Timeout: sessionPullRequestRefreshTimeout},
	); err != nil {
		d.logf("session pull requests: register settle deadline: %v", err)
	}
	if err := runner.RegisterCron(
		sessionPullRequestRefreshKind,
		sessionPullRequestRefreshTick,
		d.sessionPullRequestRefreshHandler,
		jobs.HandlerConfig{Timeout: sessionPullRequestRefreshTimeout},
	); err != nil {
		d.logf("session pull requests: register refresh tick: %v", err)
	}
}

func (d *Daemon) sessionPullRequestRefreshHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	fetched, changed := d.refreshSessionPullRequestsContext(ctx, time.Now(), "")
	return map[string]any{"fetched": fetched, "changed": changed}, context.Cause(ctx)
}

type sessionPullRequestSettlePayload struct {
	PRID string `json:"pr_id"`
}

func (d *Daemon) sessionPullRequestSettleHandler(ctx context.Context, job *jobs.Job) (any, error) {
	var payload sessionPullRequestSettlePayload
	if err := job.DecodePayload(&payload); err != nil {
		return nil, err
	}
	fetched, changed := d.refreshSessionPullRequestsContext(ctx, time.Now(), payload.PRID)
	return map[string]any{"fetched": fetched, "changed": changed}, context.Cause(ctx)
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
	return d.refreshSessionPullRequestsContext(context.Background(), now, "")
}

func (d *Daemon) refreshSessionPullRequestsContext(ctx context.Context, now time.Time, forcePRID string) (fetched, changed int) {
	d.sessionPRRefreshMu.Lock()
	defer d.sessionPRRefreshMu.Unlock()
	if d.store == nil {
		return 0, 0
	}
	records := d.store.OpenSessionPullRequests()
	watches := d.store.PullRequestWatches()
	watchedRecords := d.store.WatchedSessionPullRequests()
	records = sessionPullRequestRecordsForWatches(records, watchedRecords, watches)
	if len(records) == 0 {
		return 0, 0
	}
	if d.hasInactiveSessionPullRequest(records) {
		schema, err := d.seedsCollection()
		if err == nil {
			var refreshable []store.SessionPullRequestRecord
			refreshable, err = d.store.OpenSessionPullRequestsReferencedBy(*schema, garden.HarvestWhenPullRequestField)
			if err == nil {
				records = refreshable
			}
		}
		if err != nil {
			if !docstore.IsUndeclaredCollection(err) {
				d.logf("session pull requests: selecting armed rows: %v", err)
			}
			records = d.activeSessionPullRequests(records)
		}
	}
	records = sessionPullRequestRecordsForWatches(records, watchedRecords, watches)
	groups := d.dueSessionPullRequests(records, watches, now, forcePRID)
	if len(groups) == 0 {
		return 0, 0
	}

	limitedRequests := make(map[string]time.Time)
	limitedResources := make(map[string]time.Time)
	var changedSessions []string
	for _, group := range groups {
		if ctx.Err() != nil {
			break
		}
		resource := "core"
		if len(group.watches) > 0 {
			resource = "graphql"
		}
		limitKey := group.host + "\x00" + resource
		if _, limited := limitedRequests[limitKey]; limited {
			continue
		}
		host, ok := d.sessionPRHostFor(group.host)
		if !ok {
			d.logf("session pull requests: no GitHub client for host %s, %s stays as recorded", group.host, group.prID)
			changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, fmt.Errorf("GitHub monitoring is unavailable for host %s", group.host), now)...)
			d.markSessionPullRequestChecked(group.prID, now)
			continue
		}
		if limited, resetAt := host.IsRateLimited(resource); limited {
			d.logf("session pull requests: %s rate limited until %s", group.host, resetAt.Format(time.RFC3339))
			limitedRequests[limitKey] = resetAt
			recordSessionPullRequestLimit(limitedResources, resource, resetAt)
			changedSessions = append(changedSessions, d.recordPullRequestWatchFailures(group, fmt.Errorf("GitHub %s rate limited until %s", resource, resetAt.Format(time.RFC3339)), now)...)
			continue
		}

		status, observation, feedbackErr, err := d.fetchSessionPullRequestStatus(ctx, host, group)
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
		if observation != nil {
			changedSessions = append(changedSessions, d.processPullRequestWatches(group, *observation, feedbackErr, status, now)...)
			if err := d.store.UpdateUnwatchedSessionPullRequestStatus(group.prID, status, now); err != nil {
				d.logf("session pull requests: store unwatched status for %s: %v", group.prID, err)
				continue
			}
		} else if err := d.store.UpdateSessionPullRequestStatus(group.prID, status, now); err != nil {
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

	d.broadcastSessionPullRequestLimits(limitedResources)
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

func (d *Daemon) dueSessionPullRequests(
	records []store.SessionPullRequestRecord, watches []store.PullRequestWatch, now time.Time, forcePRID string,
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
		group.due = group.due || rec.PRID == forcePRID || sessionPullRequestDueForWatch(rec, len(group.watches) > 0, now)
	}

	var due []*sessionPullRequestGroup
	for _, group := range groups {
		if group.due {
			due = append(due, group)
		}
	}
	return due
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

func (d *Daemon) fetchSessionPullRequestStatus(ctx context.Context, host sessionPRHost, group *sessionPullRequestGroup) (store.SessionPullRequestStatus, *prreadiness.Observation, error, error) {
	if len(group.watches) > 0 {
		readinessHost, ok := host.(sessionPRReadinessHost)
		if !ok {
			return store.SessionPullRequestStatus{}, nil, nil, errors.New("GitHub host does not support pull request readiness")
		}
		observation, err := readinessHost.FetchPullRequestReadiness(ctx, group.repo, group.number)
		if err != nil {
			return store.SessionPullRequestStatus{}, nil, nil, err
		}
		feedback, threads, feedbackErr := readinessHost.FetchPullRequestFeedback(ctx, group.repo, group.number)
		if feedbackErr == nil {
			observation.Feedback = feedback
			observation.Threads = threads
			observation.FeedbackComplete = true
		}
		status := group.previous
		status.Title = observation.Title
		status.Draft = observation.Draft
		status.State = sessionPullRequestStateFromObservation(observation)
		status.HeadSHA = observation.HeadSHA
		status.HeadBranch = observation.HeadRef
		status.MergeableState = strings.ToLower(strings.TrimSpace(observation.MergeStateStatus))
		status.CIStatus = sessionPullRequestCIStatus(observation.Checks)
		status.ReviewStatus = sessionPullRequestReviewStatus(observation.ReviewDecision)
		return status, observation, feedbackErr, nil
	}
	snapshot, err := host.FetchPullRequestSnapshot(group.repo, group.number)
	if err != nil {
		return store.SessionPullRequestStatus{}, nil, nil, err
	}

	status := group.previous
	status.Title = snapshot.Title
	status.Draft = snapshot.Draft
	status.State = sessionPullRequestStateFromSnapshot(snapshot)
	status.HeadSHA = snapshot.HeadSHA
	status.HeadBranch = snapshot.HeadRef
	if status.State != sessionPullRequestOpen {
		return status, nil, nil, nil
	}

	status.MergeableState = snapshot.MergeableState
	status.CIStatus = github.CIStatusFromMergeableState(snapshot.MergeableState)
	review, err := host.FetchPullRequestReviewStatus(group.repo, group.number)
	if err != nil {
		d.logf("session pull requests: reviews for %s: %v", group.prID, err)
		return status, nil, nil, nil
	}
	status.ReviewStatus = review
	return status, nil, nil, nil
}

func sessionPullRequestCIStatus(checks []prreadiness.Check) string {
	if len(checks) == 0 {
		return "none"
	}
	result := "success"
	for _, check := range checks {
		switch strings.ToLower(check.State) {
		case "failure", "failed":
			return "failure"
		case "pending":
			result = "pending"
		}
	}
	return result
}

func sessionPullRequestReviewStatus(decision string) string {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "REVIEW_REQUIRED":
		return "pending"
	case "":
		return "none"
	default:
		return "unavailable"
	}
}

func sessionPullRequestStateFromObservation(observation *prreadiness.Observation) string {
	if strings.EqualFold(observation.State, sessionPullRequestOpen) {
		return sessionPullRequestOpen
	}
	if observation.Merged || strings.EqualFold(observation.State, sessionPullRequestMerged) {
		return sessionPullRequestMerged
	}
	return sessionPullRequestClosed
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

func sessionPullRequestRefreshIntervalForWatch(rec store.SessionPullRequestRecord, watched bool, now time.Time) time.Duration {
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

func sessionPullRequestDueForWatch(rec store.SessionPullRequestRecord, watched bool, now time.Time) bool {
	checked := protocol.Timestamp(rec.StatusCheckedAt).Time()
	if checked.IsZero() {
		return true
	}
	return now.Sub(checked) >= sessionPullRequestRefreshIntervalForWatch(rec, watched, now)
}

func sessionPullRequestRecordsForWatches(selected, all []store.SessionPullRequestRecord, watches []store.PullRequestWatch) []store.SessionPullRequestRecord {
	included := make(map[string]bool, len(selected))
	for _, record := range selected {
		included[record.SessionID+"\x00"+record.PRID] = true
	}
	watched := make(map[string]bool, len(watches))
	for _, watch := range watches {
		watched[watch.SessionID+"\x00"+watch.PRID] = true
	}
	for _, record := range all {
		key := record.SessionID + "\x00" + record.PRID
		if watched[key] && !included[key] {
			selected = append(selected, record)
			included[key] = true
		}
	}
	return selected
}

func (d *Daemon) recordPullRequestWatchFailures(group *sessionPullRequestGroup, fetchErr error, now time.Time) []string {
	changed := make([]string, 0, len(group.watches))
	for _, watch := range group.watches {
		item := pullRequestWatchMailboxItem(
			watch, "outage:"+now.UTC().Format(time.RFC3339Nano), store.PullRequestWatchOutageCoalesceKey(watch.PRID),
			"monitoring is delayed", []string{fetchErr.Error()}, now,
		)
		delivery, err := d.store.RecordPullRequestWatchFailure(
			watch.SessionID, watch.PRID, watch.CreatedAt, watch.Mode, watch.Reviewer,
			fetchErr.Error(), item, now,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			d.logf("pull request watch: record failure for %s/%s: %v", watch.SessionID, watch.PRID, err)
			continue
		}
		if delivery != nil {
			d.deliverPullRequestMailbox(*delivery)
		}
		changed = append(changed, watch.SessionID)
	}
	return changed
}

func (d *Daemon) processPullRequestWatches(
	group *sessionPullRequestGroup,
	observation prreadiness.Observation,
	feedbackErr error,
	status store.SessionPullRequestStatus,
	now time.Time,
) []string {
	changed := make([]string, 0, len(group.watches))
	for _, watch := range group.watches {
		transition := prreadiness.Advance(watch.Cursor, observation, watch.Mode, watch.Reviewer, now)
		health := "current"
		healthError := ""
		feedbackError := ""
		if feedbackErr != nil {
			health = "delayed"
			healthError = "feedback: " + feedbackErr.Error()
			feedbackError = feedbackErr.Error()
		}
		items := make([]agentmailbox.Item, 0, len(transition.Actions))
		for _, action := range transition.Actions {
			coalesceKey := store.PullRequestWatchCoalesceKey(watch.PRID)
			kind := pullRequestWatchActionKind(action.Kind)
			details := append([]string(nil), action.Details...)
			if action.Kind == "thread_reopened" {
				coalesceKey = ""
			}
			if action.Feedback != nil {
				coalesceKey = ""
				kind = "new " + strings.ReplaceAll(action.Kind, "_", " ")
				detail := strings.TrimSpace(action.Feedback.Body)
				if action.Feedback.Location != "" {
					detail = action.Feedback.Location + ": " + detail
				}
				details = []string{strings.TrimSpace(action.Feedback.Author + ": " + detail)}
			}
			items = append(items, pullRequestWatchMailboxItem(watch, action.ID, coalesceKey, kind, details, now))
		}
		terminal := transition.Evaluation.State == prreadiness.StateMerged || transition.Evaluation.State == prreadiness.StateClosed
		deliveries, err := d.store.ReconcilePullRequestWatch(store.PullRequestWatchReconcile{
			SessionID: watch.SessionID, PRID: watch.PRID, CreatedAt: watch.CreatedAt,
			Mode: watch.Mode, Reviewer: watch.Reviewer, Cursor: transition.Cursor,
			Status: status, Evaluation: transition.Evaluation,
			Health: health, HealthError: healthError, FeedbackError: feedbackError,
			ClearAction: watch.Cursor.LastAction != "" && transition.Cursor.LastAction == "",
			Terminal:    terminal, MailboxItems: items, At: now,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			d.logf("pull request watch: reconcile %s/%s: %v", watch.SessionID, watch.PRID, err)
			continue
		}
		for _, delivery := range deliveries {
			d.deliverPullRequestMailbox(delivery)
		}
		if transition.Evaluation.SettlingUntil != nil {
			d.schedulePullRequestSettle(watch, *transition.Evaluation.SettlingUntil, now)
		}
		changed = append(changed, watch.SessionID)
	}
	return changed
}

func (d *Daemon) deliverPullRequestMailbox(delivery agentmailbox.Delivery) {
	if err := d.deliverAgentMailboxItem(delivery); err != nil &&
		!errors.Is(err, errAgentMailboxDoorbellOutstanding) &&
		!errors.Is(err, errAgentMailboxDoorbellInFlight) {
		d.logf("pull request watch: deliver %s: %v", delivery.Item.ID, err)
	}
}

func (d *Daemon) schedulePullRequestSettle(watch store.PullRequestWatch, deadline, now time.Time) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	delay := deadline.Sub(now)
	if delay < 0 {
		delay = 0
	}
	_, err := runner.Enqueue(sessionPullRequestSettleKind, jobs.EnqueueOptions{
		UniqueKey: watch.PRID,
		Payload:   sessionPullRequestSettlePayload{PRID: watch.PRID},
		Delay:     delay,
	})
	if err != nil {
		d.logf("pull request watch: schedule Codex deadline for %s/%s: %v", watch.SessionID, watch.PRID, err)
	}
}

func (d *Daemon) schedulePullRequestRefreshNow(sessionID, prID string) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	if _, err := runner.Enqueue(sessionPullRequestSettleKind, jobs.EnqueueOptions{
		UniqueKey: prID,
		Payload:   sessionPullRequestSettlePayload{PRID: prID},
		RunNow:    true,
	}); err != nil {
		d.logf("pull request watch: schedule initial refresh for %s/%s: %v", sessionID, prID, err)
	}
}

func pullRequestWatchMailboxItem(
	watch store.PullRequestWatch,
	eventID, coalesceKey, kind string,
	details []string,
	now time.Time,
) agentmailbox.Item {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
		"pull-request-watch", watch.SessionID, watch.PRID, watch.CreatedAt, eventID,
	}, "\x00"))).String()
	details = append([]string(nil), details...)
	sort.Strings(details)
	prompt := fmt.Sprintf("Pull request monitor: %s for %s.", kind, watch.PRID)
	for _, detail := range details {
		if detail = strings.TrimSpace(detail); detail != "" {
			prompt += "\n- " + detail
		}
	}
	prompt += "\nCheck the current pull request before acting. This notification grants no merge authority."
	return agentmailbox.Item{
		ID: id, RecipientSessionID: watch.SessionID, Kind: agentmailbox.KindMaintenancePrompt,
		SourceID: watch.PRID, CoalesceKey: coalesceKey,
		Hint: "pull request update", Prompt: prompt,
		CreatedAt: now.UTC().Format(docstore.TimeFormat),
	}
}

func pullRequestWatchActionKind(kind string) string {
	switch kind {
	case "ready":
		return "ready"
	case "checks_failed":
		return "checks failed"
	case "changes_requested":
		return "changes requested"
	case "thread_reopened":
		return "review thread reopened"
	case "merged":
		return "merged"
	case "closed":
		return "closed"
	default:
		return strings.ReplaceAll(kind, "_", " ")
	}
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

func (d *Daemon) reheatSessionPullRequest(event bus.Event) {
	if d.store == nil {
		return
	}
	if err := d.store.TouchSessionPullRequestActivity(event.Subject, time.Now()); err != nil {
		d.logf("session pull requests: reheat %s: %v", event.Subject, err)
	}
}
