package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const automationReviewWithdrawnMessage = "GitHub review request withdrawn before delivery"

func (d *Daemon) automationRunPullRequest(ctx context.Context, definitionID int, requestID, rawURL string) (*store.AutomationRun, error) {
	if err := d.requireHome(automation.Surface); err != nil {
		return nil, err
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, errors.New("request_id is required")
	}
	if existing, err := d.store.GetManualAutomationRun(definitionID, requestID); err != nil {
		return nil, err
	} else if existing != nil {
		occurrence, err := d.store.GetAutomationOccurrence(existing.OccurrenceID)
		if err != nil || occurrence == nil {
			return nil, errors.Join(errors.New("existing automation occurrence missing"), err)
		}
		return d.automationRun(ctx, definitionID, requestID, occurrence.PayloadJSON)
	}
	def, err := d.store.GetAutomationDefinition(definitionID)
	if err != nil || def == nil {
		if err == nil {
			err = fmt.Errorf("automation %d not found", definitionID)
		}
		return nil, err
	}
	var spec automation.DefinitionSpec
	if err := json.Unmarshal([]byte(def.SpecJSON), &spec); err != nil {
		return nil, err
	}
	if spec.Trigger.Type != "manual" {
		return nil, fmt.Errorf("automation %d is provider-driven and cannot be run manually yet", definitionID)
	}
	if spec.Location.Type != "repository_worktree" {
		return nil, errors.New("--pr-url requires a repository_worktree automation")
	}
	host, owner, repository, number, err := automation.ParsePullRequestURL(rawURL)
	if err != nil {
		return nil, err
	}
	if d.ghRegistry == nil {
		return nil, fmt.Errorf("GitHub host %s is not authenticated", host)
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return nil, fmt.Errorf("GitHub host %s is not authenticated", host)
	}
	snapshot, err := client.FetchPullRequestSnapshot(owner+"/"+repository, number)
	if err != nil {
		return nil, err
	}
	if snapshot.Number != number || !strings.EqualFold(snapshot.BaseRepository, owner+"/"+repository) {
		return nil, errors.New("GitHub response does not match requested pull request")
	}
	if snapshot.State != "open" {
		return nil, fmt.Errorf("pull request is %s; only open pull requests can be reviewed", snapshot.State)
	}
	input := pullRequestAutomationInput(host, owner, repository, snapshot)
	canonical, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if _, err := automation.ParsePullRequestInput(canonical); err != nil {
		return nil, err
	}
	return d.automationRun(ctx, definitionID, requestID, string(canonical))
}
func pullRequestAutomationInput(host, owner, repository string, snapshot *github.PullRequestSnapshot) automation.PullRequestInput {
	return automation.PullRequestInput{
		Provider: "github", Host: host, Owner: owner, Repository: repository, Number: snapshot.Number,
		URL: strings.TrimSuffix(snapshot.URL, "/"), Title: snapshot.Title, Body: snapshot.Body,
		Author: snapshot.Author, Draft: snapshot.Draft, State: snapshot.State,
		HeadSHA: snapshot.HeadSHA, HeadRef: snapshot.HeadRef, HeadRepository: snapshot.HeadRepository,
		BaseSHA: snapshot.BaseSHA, BaseRef: snapshot.BaseRef,
	}
}

func (d *Daemon) observeGitHubReviewRequests(host string, prs []*protocol.PR, observedAt time.Time) {
	release, held := d.life.Hold("observeGitHubReviewRequests")
	if !held {
		return
	}
	defer release()
	definitions, err := d.store.ListAutomationDefinitions()
	if err != nil {
		d.logf("automation GitHub observation list definitions: %v", err)
		return
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok {
		return
	}
	for i := range definitions {
		definition := definitions[i]
		if !definition.Enabled {
			continue
		}
		var spec automation.DefinitionSpec
		if err := json.Unmarshal([]byte(definition.SpecJSON), &spec); err != nil {
			d.logf("automation GitHub observation parse %d: %v", definition.ID, err)
			continue
		}
		if spec.Trigger.Type != "github_review_requested" {
			continue
		}
		bySubject := make(map[string]*protocol.PR)
		var observations []store.AutomationReviewRequestObservation
		for _, pr := range prs {
			if pr == nil || pr.ApprovedByMe || pr.Role != protocol.PRRoleReviewer || pr.Reason != protocol.PRReasonReviewNeeded || pr.State != protocol.PRStateWaiting {
				continue
			}
			identity, err := automation.CanonicalRepositoryIdentity(host + "/" + pr.Repo)
			if err != nil || !spec.Trigger.Repositories.Matches(identity) {
				continue
			}
			subject := identity + "#" + fmt.Sprint(pr.Number)
			if _, exists := bySubject[subject]; exists {
				continue
			}
			bySubject[subject] = pr
			observations = append(observations, store.AutomationReviewRequestObservation{
				SubjectKey: subject,
				HeadSHA:    protocol.Deref(pr.HeadSHA),
			})
		}
		candidates, err := d.reconcileAutomationReviewRequestHeads(definition.ID, host, observations, observedAt)
		if err != nil {
			d.logf("automation GitHub observation reconcile %d: %v", definition.ID, err)
			continue
		}
		if len(candidates) > 0 {
			d.life.Go("deliverGitHubReviewRequests", func() {
				d.deliverGitHubReviewRequests(host, client, definition, spec, candidates, bySubject, observedAt)
			})
		}
	}
}

func (d *Daemon) deliverGitHubReviewRequests(host string, client *github.Client, definition store.AutomationDefinition, spec automation.DefinitionSpec, candidates []store.AutomationReviewRequestCandidate, bySubject map[string]*protocol.PR, observedAt time.Time) {
	for _, candidate := range candidates {
		if d.stopping() {
			return
		}
		pr := bySubject[candidate.SubjectKey]
		if pr == nil {
			continue
		}
		run, err := d.claimReviewRequest(host, client, definition, spec, candidate, pr, observedAt)
		if err != nil {
			d.logf("automation GitHub observation %s: %v", candidate.SubjectKey, err)
			continue
		}
		if run == nil {
			continue
		}
		d.broadcastAutomationsChanged(definition.ID)
		if err := d.deliverClaimedReviewRun(run); err != nil {
			d.logf("automation GitHub observation deliver %s: %v", candidate.SubjectKey, err)
		}
	}
}

func (d *Daemon) claimReviewRequest(host string, client *github.Client, definition store.AutomationDefinition, spec automation.DefinitionSpec, candidate store.AutomationReviewRequestCandidate, pr *protocol.PR, observedAt time.Time) (*store.AutomationRun, error) {
	observationLock := d.automationObservationLock(definition.ID, candidate.SubjectKey, candidate.Cycle)
	observationLock.Lock()
	defer observationLock.Unlock()

	needsClaim, err := d.store.AutomationReviewRequestHeadNeedsClaim(definition.ID, candidate.SubjectKey, candidate.Cycle, candidate.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("recheck claim: %w", err)
	}
	if !needsClaim {
		return nil, nil
	}
	repositoryParts := strings.Split(pr.Repo, "/")
	if len(repositoryParts) != 2 {
		return nil, fmt.Errorf("invalid repository %q", pr.Repo)
	}
	providerSnapshot, err := client.FetchPullRequestSnapshot(pr.Repo, pr.Number)
	if err != nil {
		return nil, fmt.Errorf("fetch pull request: %w", err)
	}
	if providerSnapshot.Number != pr.Number || !strings.EqualFold(providerSnapshot.BaseRepository, pr.Repo) || providerSnapshot.State != "open" || providerSnapshot.Draft {
		return nil, errors.New("ignored mismatched pull request snapshot")
	}
	input := pullRequestAutomationInput(host, repositoryParts[0], repositoryParts[1], providerSnapshot)
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal pull request input: %w", err)
	}
	if _, err := automation.ParsePullRequestInput(payload); err != nil {
		return nil, fmt.Errorf("invalid pull request snapshot: %w", err)
	}
	effective, err := automation.Effective(spec, definition.Revision)
	if err != nil {
		return nil, fmt.Errorf("build effective definition: %w", err)
	}
	snapshotJSON, err := json.Marshal(effective)
	if err != nil {
		return nil, fmt.Errorf("marshal effective definition: %w", err)
	}
	reservation, err := d.newAutomationRunReservation(&definition)
	if err != nil {
		return nil, fmt.Errorf("reserve run: %w", err)
	}
	run, _, err := d.store.ClaimGitHubReviewAutomationRun(definition.ID, candidate.SubjectKey, candidate.Cycle, definition.Revision, string(payload), string(snapshotJSON), observedAt, reservation)
	if err != nil {
		return nil, fmt.Errorf("claim run: %w", err)
	}
	return run, nil
}

func (d *Daemon) deliverClaimedReviewRun(run *store.AutomationRun) error {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	current, err := d.store.GetAutomationRun(run.ID)
	if err != nil || current == nil || current.State != store.AutomationRunStatePending {
		return err
	}
	if err := d.deliverObservedAutomationRun(current); err != nil {
		_, err = d.handleAutomationDeliveryError(current, err)
		return err
	}
	return nil
}

func (d *Daemon) reconcileAutomationReviewRequests(definitionID int, host string, subjects []string, observedAt time.Time) ([]store.AutomationReviewRequestCandidate, error) {
	observations := make([]store.AutomationReviewRequestObservation, 0, len(subjects))
	for _, subject := range subjects {
		observations = append(observations, store.AutomationReviewRequestObservation{SubjectKey: subject})
	}
	return d.reconcileAutomationReviewRequestHeads(definitionID, host, observations, observedAt)
}

func (d *Daemon) reconcileAutomationReviewRequestHeads(definitionID int, host string, observations []store.AutomationReviewRequestObservation, observedAt time.Time) ([]store.AutomationReviewRequestCandidate, error) {
	d.automationMu.Lock()
	defer d.automationMu.Unlock()
	if err := d.settleWithdrawnAutomationRuns(definitionID, host); err != nil {
		return nil, err
	}
	candidates, err := d.store.ReconcileAutomationReviewRequestHeads(definitionID, host, observations, observedAt)
	if err != nil {
		return nil, err
	}
	if err := d.settleWithdrawnAutomationRuns(definitionID, host); err != nil {
		return nil, err
	}
	return candidates, nil
}
func (d *Daemon) settleWithdrawnAutomationRuns(definitionID int, host string) error {
	withdrawn, err := d.store.ListWithdrawnGitHubReviewUndeliveredRuns(definitionID, host)
	if err != nil {
		return err
	}
	var cancelErr error
	for i := range withdrawn {
		if err := d.settleWithdrawnAutomationRun(&withdrawn[i]); err != nil {
			cancelErr = errors.Join(cancelErr, err)
		}
	}
	return cancelErr
}
func (d *Daemon) settleWithdrawnAutomationRun(run *store.AutomationRun) error {
	continuation, err := d.automationRunIsContinuation(run)
	if err != nil {
		return err
	}
	if !continuation && run.State == store.AutomationRunStatePending && d.hasAutomationSession(run.SessionID) {
		if deliverErr := d.deliverObservedAutomationRun(run); deliverErr != nil {
			if _, err := d.handleAutomationDeliveryError(run, deliverErr); err != nil {
				d.logf("automation deliver started reviewer %s: %v", run.ID, err)
			}
		}
		return nil
	}
	if run.State == store.AutomationRunStateCancelled && run.CancelReason == store.AutomationCancelReasonReviewWithdrawn {
		return d.recordAutomationRunSeedOutcome(run, automationFailureComment(run, automationReviewWithdrawnMessage))
	}
	_, cancelErr := d.cancelAutomationRun(run, store.AutomationCancelReasonReviewWithdrawn, automationReviewWithdrawnMessage)
	return cancelErr
}
func (d *Daemon) hasAutomationSession(sessionID string) bool {
	if d.store.Get(sessionID) != nil {
		return true
	}
	return d.sessionLive(context.Background(), sessionID)
}
