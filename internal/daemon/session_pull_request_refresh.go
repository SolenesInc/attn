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
	FetchPullRequestReadiness(repo string, number int) (*github.PullRequestReadiness, error)
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

		observedAt := time.Now()
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
		changedSessions = append(changedSessions, d.processPullRequestWatches(group, readiness, observedAt)...)
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

func (d *Daemon) fetchSessionPullRequestStatus(host sessionPRHost, group *sessionPullRequestGroup) (store.SessionPullRequestStatus, *github.PullRequestReadiness, error) {
	if len(group.watches) > 0 {
		readiness, err := host.FetchPullRequestReadiness(group.repo, group.number)
		if err != nil {
			return store.SessionPullRequestStatus{}, nil, err
		}
		status := group.previous
		status.Title = readiness.Snapshot.Title
		status.Draft = readiness.Snapshot.Draft
		status.State = sessionPullRequestStateFromSnapshot(readiness.Snapshot)
		status.HeadSHA = readiness.Snapshot.HeadSHA
		status.HeadBranch = readiness.Snapshot.HeadRef
		if status.State == sessionPullRequestOpen {
			status.MergeableState = readiness.Snapshot.MergeableState
			status.CIStatus = sessionPullRequestCIStatus(readiness.Evidence.CheckState)
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

func sessionPullRequestCIStatus(state string) string {
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

func (d *Daemon) processPullRequestWatches(group *sessionPullRequestGroup, readiness *github.PullRequestReadiness, now time.Time) (changedSessions []string) {
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
		evidence := readiness.Evidence
		evidence.HeadObservedAt = pullRequestWatchHeadObservedAt(watch, readiness.Snapshot.HeadSHA, now)
		evaluation := prreadiness.Evaluate(evidence, watch.Reviewer)
		if err := d.store.UpdateSessionPullRequestReviewStatus(watch.SessionID, watch.PRID, evaluation.ReviewState); err != nil {
			d.logf("session pull requests: store review status for %s/%s: %v", watch.SessionID, watch.PRID, err)
		}
		if watch.LastHeadSHA != "" && watch.LastHeadSHA != readiness.Snapshot.HeadSHA {
			if _, err := d.store.DeleteUnreadMaintenanceMailboxItem(watch.SessionID, pullRequestWatchCoalesceKey(watch.PRID)); err != nil {
				d.logf("pull request watch: invalidate stale inbox item for %s/%s: %v", watch.SessionID, watch.PRID, err)
			}
			d.refreshAgentMailboxUnread(watch.SessionID)
		}

		observation := pullRequestWatchAction(readiness, evaluation, watch)
		if err := d.notifyPullRequestWatchFeedback(watch, observation.Comments, now); err != nil {
			continue
		}
		key := ""
		if observation.Kind != "" {
			key = prreadiness.Fingerprint(observation.Kind, readiness.Snapshot.HeadSHA, observation.Details)
			if key != watch.LastObservationKey || watch.LastError != "" {
				if err := d.notifyPullRequestWatch(watch, observation.Kind, observation.Details, now); err != nil {
					continue
				}
			}
		} else if _, err := d.store.DeleteUnreadMaintenanceMailboxItem(watch.SessionID, pullRequestWatchCoalesceKey(watch.PRID)); err != nil {
			d.logf("pull request watch: clear inactive inbox item for %s/%s: %v", watch.SessionID, watch.PRID, err)
		}
		if err := d.store.RecordPullRequestWatchSuccess(
			watch.SessionID, watch.PRID, readiness.Snapshot.HeadSHA, key, observation.Feedback, now,
		); err != nil {
			d.logf("pull request watch: record observation for %s/%s: %v", watch.SessionID, watch.PRID, err)
		}
		if readiness.Snapshot.State != sessionPullRequestOpen {
			if _, err := d.store.UnwatchPullRequest(watch.SessionID, watch.PRID); err != nil {
				d.logf("pull request watch: stop completed watch %s/%s: %v", watch.SessionID, watch.PRID, err)
			}
		}
		d.refreshAgentMailboxUnread(watch.SessionID)
		changedSessions = append(changedSessions, watch.SessionID)
	}
	return changedSessions
}

func pullRequestWatchHeadObservedAt(watch store.PullRequestWatch, head string, now time.Time) time.Time {
	if watch.LastHeadSHA != head || watch.HeadObservedAt == "" {
		return now
	}
	at, err := time.Parse(time.RFC3339Nano, watch.HeadObservedAt)
	if err != nil {
		return now
	}
	return at
}

func samePullRequestWatchGeneration(left, right store.PullRequestWatch) bool {
	return left.SessionID == right.SessionID && left.PRID == right.PRID &&
		left.Reviewer == right.Reviewer && left.CreatedAt == right.CreatedAt
}

type watchObservation struct {
	Kind     string
	Details  []string
	Feedback store.PullRequestFeedbackCursor
	Comments []prreadiness.Comment
}

func pullRequestWatchAction(
	readiness *github.PullRequestReadiness, evaluation prreadiness.Evaluation, watch store.PullRequestWatch,
) watchObservation {
	feedback, cursor := pullRequestWatchFeedback(readiness.Evidence, evaluation, watch)
	if readiness.Snapshot.State != sessionPullRequestOpen {
		state := sessionPullRequestStateFromSnapshot(readiness.Snapshot)
		return watchObservation{Kind: state, Details: []string{"pull request is " + state}, Feedback: cursor, Comments: feedback}
	}
	var kinds []string
	var details []string
	if readiness.Evidence.CheckState == prreadiness.ChecksFailed {
		kinds = append(kinds, "checks failed")
		details = append(details, readiness.Evidence.FailedChecks...)
	}
	if evaluation.ReviewState == prreadiness.ReviewUnavailable {
		kinds = append(kinds, "review unavailable")
		details = append(details, evaluation.UnavailableCause)
	}
	findings := uniquePullRequestWatchFindings(evaluation.Findings, evaluation.Unresolved)
	if len(findings) > 0 || evaluation.ReviewState == prreadiness.ReviewChangesRequested {
		findingDetails := make([]string, 0, len(findings)+1)
		for _, finding := range findings {
			line := strings.TrimSpace(finding.Body)
			if finding.Location != "" {
				line = finding.Location + ": " + line
			}
			if line != "" {
				findingDetails = append(findingDetails, line)
			}
		}
		if len(findingDetails) == 0 && evaluation.ReviewBody != "" {
			findingDetails = append(findingDetails, evaluation.ReviewBody)
		}
		kinds = append(kinds, "review findings")
		details = append(details, findingDetails...)
	}
	if evaluation.Ready {
		kinds = append(kinds, "ready")
		details = append(details, "checks passed", watch.Reviewer+" reviewed the current head")
	}
	return watchObservation{Kind: strings.Join(kinds, " and "), Details: details, Feedback: cursor, Comments: feedback}
}

func uniquePullRequestWatchFindings(groups ...[]prreadiness.Finding) []prreadiness.Finding {
	var findings []prreadiness.Finding
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, finding := range group {
			key := strings.TrimSpace(finding.ID)
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(finding.Location)) + "\x00" +
					strings.ToLower(strings.Join(strings.Fields(finding.Body), " "))
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, finding)
		}
	}
	return findings
}

func pullRequestWatchFeedback(evidence prreadiness.Evidence, evaluation prreadiness.Evaluation, watch store.PullRequestWatch) ([]prreadiness.Comment, store.PullRequestFeedbackCursor) {
	represented := make(map[string]bool)
	for _, finding := range uniquePullRequestWatchFindings(evaluation.Findings, evaluation.Unresolved) {
		represented[finding.ID] = true
	}
	for _, review := range evidence.Reviews {
		if samePullRequestWatchActor(review.Author, watch.Reviewer) && review.SubmittedAt.Equal(evaluation.ReviewSubmitted) {
			represented[review.ID] = true
		}
	}
	seenAt, err := time.Parse(time.RFC3339Nano, watch.FeedbackSeenAt)
	if err != nil {
		seenAt, _ = time.Parse(time.RFC3339Nano, watch.CreatedAt)
	}
	seenIDs := make(map[string]bool, len(watch.FeedbackSeenIDs))
	for _, id := range watch.FeedbackSeenIDs {
		seenIDs[id] = true
	}
	nextAt := seenAt
	nextIDs := append([]string(nil), watch.FeedbackSeenIDs...)
	var feedback []prreadiness.Comment
	for _, comment := range evidence.Comments {
		if comment.Bot || comment.CreatedAt.Before(seenAt) ||
			(comment.CreatedAt.Equal(seenAt) && seenIDs[comment.ID]) {
			continue
		}
		if !represented[comment.ID] {
			feedback = append(feedback, comment)
		}
		switch {
		case comment.CreatedAt.After(nextAt):
			nextAt = comment.CreatedAt
			nextIDs = []string{comment.ID}
		case comment.CreatedAt.Equal(nextAt):
			nextIDs = append(nextIDs, comment.ID)
		}
	}
	sort.Strings(nextIDs)
	return feedback, store.PullRequestFeedbackCursor{SeenAt: nextAt, SeenIDs: nextIDs}
}

func (d *Daemon) notifyPullRequestWatchFeedback(watch store.PullRequestWatch, comments []prreadiness.Comment, now time.Time) error {
	for _, comment := range comments {
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
			"pull-request-feedback", watch.SessionID, watch.PRID, comment.ID,
		}, "\x00"))).String()
		if err := d.queuePullRequestWatchNotification(watch, id, "", "human feedback", []string{comment.Author + ": " + strings.TrimSpace(comment.Body)}, now); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) notifyPullRequestWatch(watch store.PullRequestWatch, kind string, details []string, now time.Time) error {
	return d.queuePullRequestWatchNotification(watch, uuid.NewString(), pullRequestWatchCoalesceKey(watch.PRID), kind, details, now)
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

func samePullRequestWatchActor(left, right string) bool {
	return strings.EqualFold(strings.TrimSuffix(left, "[bot]"), strings.TrimSuffix(right, "[bot]"))
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
