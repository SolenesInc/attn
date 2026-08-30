package daemon

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func gardenReviewToProtocol(run garden.ReviewRun, items []garden.ReviewItem) protocol.GardenReview {
	wireItems := make([]protocol.GardenReviewItem, 0, len(items))
	for _, item := range items {
		wireItems = append(wireItems, gardenReviewItemToProtocol(item))
	}
	return protocol.GardenReview{Run: gardenReviewRunToProtocol(run), Items: wireItems}
}

func gardenReviewRunToProtocol(run garden.ReviewRun) protocol.GardenReviewRun {
	wire := protocol.GardenReviewRun{
		ID: run.ID, CandidateIds: run.CandidateIDs,
		Recipe: protocol.GardenReviewRecipe{
			Agent: run.Recipe.Agent, Model: run.Recipe.Model,
		},
		Status: run.Status, CapturedAt: run.CapturedAt,
	}
	if run.Recipe.Effort != "" {
		wire.Recipe.Effort = protocol.Ptr(run.Recipe.Effort)
	}
	if run.CompletedAt != "" {
		wire.CompletedAt = protocol.Ptr(run.CompletedAt)
	}
	return wire
}

func gardenReviewItemToProtocol(item garden.ReviewItem) protocol.GardenReviewItem {
	evidence := make([]protocol.GardenReviewEvidence, 0, len(item.Evidence))
	for _, entry := range item.Evidence {
		evidence = append(evidence, protocol.GardenReviewEvidence{Label: entry.Label, Text: entry.Text})
	}
	wire := protocol.GardenReviewItem{
		ID: item.ID, RunID: item.RunID, SeedID: item.SeedID, SeedRev: int(item.SeedRev),
		EvidenceVersion: item.EvidenceVersion, Title: item.Title, Body: item.Body,
		Evidence: evidence, Actions: item.Actions, Status: item.Status, Resolution: item.Resolution,
		CitedEvidence: item.CitedEvidence,
	}
	if item.Recommendation != "" {
		wire.Recommendation = protocol.Ptr(item.Recommendation)
	}
	if item.Explanation != "" {
		wire.Explanation = protocol.Ptr(item.Explanation)
	}
	if item.Error != "" {
		wire.Error = protocol.Ptr(item.Error)
	}
	if item.StartedAt != "" {
		wire.StartedAt = protocol.Ptr(item.StartedAt)
	}
	if item.CompletedAt != "" {
		wire.CompletedAt = protocol.Ptr(item.CompletedAt)
	}
	if item.ResolvedAction != "" {
		wire.ResolvedAction = protocol.Ptr(item.ResolvedAction)
	}
	if item.ReviewAgainAt != "" {
		wire.ReviewAgainAt = protocol.Ptr(item.ReviewAgainAt)
	}
	if item.AdvisorState != "" {
		wire.AdvisorState = protocol.Ptr(item.AdvisorState)
	}
	if item.AdvisorAttempt > 0 {
		wire.AdvisorAttempt = protocol.Ptr(item.AdvisorAttempt)
	}
	if item.AdvisorMaxAttempts > 0 {
		wire.AdvisorMaxAttempts = protocol.Ptr(item.AdvisorMaxAttempts)
	}
	if item.AdvisorRetryAt != "" {
		wire.AdvisorRetryAt = protocol.Ptr(item.AdvisorRetryAt)
	}
	if item.AdvisorUpdatedAt != "" {
		wire.AdvisorUpdatedAt = protocol.Ptr(item.AdvisorUpdatedAt)
	}
	if item.AdvisorError != "" {
		wire.AdvisorError = protocol.Ptr(item.AdvisorError)
	}
	return wire
}

func (d *Daemon) handleSeedReviewStart(conn net.Conn, _ *protocol.SeedReviewStartMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "review start", err)
		return
	}
	run, items, err := d.startGardenReview()
	d.sendSeedReviewResponse(conn, "start", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewShow(conn net.Conn, msg *protocol.SeedReviewShowMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "review show", err)
		return
	}
	reviewID := protocol.Deref(msg.ReviewID)
	if reviewID == "" {
		run, items, count, err := d.gardenReviewOverview()
		d.sendSeedReviewResponse(conn, "show", run, items, count, err)
		return
	}
	run, items, err := d.showGardenReview(reviewID)
	d.sendSeedReviewResponse(conn, "show", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewCancel(conn net.Conn, msg *protocol.SeedReviewCancelMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "review cancel", err)
		return
	}
	run, items, err := d.cancelGardenReview(msg.ReviewID)
	d.sendSeedReviewResponse(conn, "cancel", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewRetry(conn net.Conn, msg *protocol.SeedReviewRetryMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "review retry", err)
		return
	}
	run, _, err := d.retryGardenReviewItem(msg.ReviewID, msg.SeedID)
	var items []garden.ReviewItem
	if err == nil {
		run, items, err = d.showGardenReview(run.ID)
	}
	d.sendSeedReviewResponse(conn, "retry", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewKeep(conn net.Conn, msg *protocol.SeedReviewKeepMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "review keep", err)
		return
	}
	run, items, err := d.keepGardenReviewItem(msg.Review, msg.SeedID)
	d.sendSeedReviewResponse(conn, "keep", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) sendSeedReviewResponse(
	conn net.Conn,
	verb string,
	run *garden.ReviewRun,
	items []garden.ReviewItem,
	candidateCount int,
	err error,
) {
	if err != nil {
		d.sendGardenError(conn, "review "+verb, err)
		return
	}
	result := protocol.SeedReviewResult{CandidateCount: candidateCount}
	if run != nil {
		review := gardenReviewToProtocol(*run, items)
		result.Review = &review
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedReviewResult: &result})
}

func (d *Daemon) handleSeedReviewStartWS(client *wsClient, msg *protocol.SeedReviewStartMessage) {
	run, items, err := d.reviewWSHomeStart()
	d.sendSeedReviewWSResult(client, protocol.Deref(msg.RequestID), "start", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) reviewWSHomeStart() (garden.ReviewRun, []garden.ReviewItem, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return garden.ReviewRun{}, nil, err
	}
	return d.startGardenReview()
}

func (d *Daemon) handleSeedReviewShowWS(client *wsClient, msg *protocol.SeedReviewShowMessage) {
	var run *garden.ReviewRun
	var items []garden.ReviewItem
	count := 0
	err := d.requireHome(garden.Surface)
	if err == nil {
		reviewID := protocol.Deref(msg.ReviewID)
		if reviewID == "" {
			run, items, count, err = d.gardenReviewOverview()
		} else {
			shown, shownItems, showErr := d.showGardenReview(reviewID)
			run, items, err = &shown, shownItems, showErr
			count = unresolvedGardenReviewItemCount(items)
		}
	}
	d.sendSeedReviewWSResult(client, protocol.Deref(msg.RequestID), "show", run, items, count, err)
}

func (d *Daemon) handleSeedReviewCancelWS(client *wsClient, msg *protocol.SeedReviewCancelMessage) {
	var run garden.ReviewRun
	var items []garden.ReviewItem
	err := d.requireHome(garden.Surface)
	if err == nil {
		run, items, err = d.cancelGardenReview(msg.ReviewID)
	}
	d.sendSeedReviewWSResult(client, protocol.Deref(msg.RequestID), "cancel", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewRetryWS(client *wsClient, msg *protocol.SeedReviewRetryMessage) {
	var run garden.ReviewRun
	var items []garden.ReviewItem
	err := d.requireHome(garden.Surface)
	if err == nil {
		run, _, err = d.retryGardenReviewItem(msg.ReviewID, msg.SeedID)
	}
	if err == nil {
		run, items, err = d.showGardenReview(run.ID)
	}
	d.sendSeedReviewWSResult(client, protocol.Deref(msg.RequestID), "retry", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) handleSeedReviewKeepWS(client *wsClient, msg *protocol.SeedReviewKeepMessage) {
	var run garden.ReviewRun
	var items []garden.ReviewItem
	err := d.requireHome(garden.Surface)
	if err == nil {
		run, items, err = d.keepGardenReviewItem(msg.Review, msg.SeedID)
	}
	d.sendSeedReviewWSResult(client, protocol.Deref(msg.RequestID), "keep", &run, items, unresolvedGardenReviewItemCount(items), err)
}

func (d *Daemon) keepGardenReviewItem(review protocol.SeedReviewActionContext, seedID string) (garden.ReviewRun, []garden.ReviewItem, error) {
	if _, err := d.validateGardenReviewAction(&review, seedID, "keep_growing"); err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if err := d.resolveGardenReviewAction(&review, seedID, "keep_growing"); err != nil {
		return garden.ReviewRun{}, nil, err
	}
	return d.showGardenReview(review.ReviewID)
}

func (d *Daemon) handleSeedReviewDraftWS(client *wsClient, msg *protocol.SeedReviewDraftMessage) {
	response := protocol.SeedReviewDraftResultMessage{
		Event: protocol.EventSeedReviewDraftResult, RequestID: msg.RequestID,
	}
	fail := func(err error) {
		response.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, response)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	item, err := d.validateGardenReviewAction(&msg.Review, msg.SeedID, "handover")
	if err != nil {
		fail(err)
		return
	}
	run, _, err := d.showGardenReview(msg.Review.ReviewID)
	if err != nil {
		fail(err)
		return
	}
	draft, err := d.draftGardenHandoffWithConfig(context.Background(), gardenAdvisorInput{
		SeedID: item.SeedID, Title: item.Title, Body: item.Body,
		Evidence: gardenAdvisorEvidenceFromReview(item.Evidence), AvailableActions: gardenAdvisorActions(item.Actions),
	}, gardenAdvisorConfig{Agent: run.Recipe.Agent, Model: run.Recipe.Model, Effort: run.Recipe.Effort})
	if err != nil {
		fail(err)
		return
	}
	if _, err := d.validateGardenReviewAction(&msg.Review, msg.SeedID, "handover"); err != nil {
		fail(err)
		return
	}
	response.Success = true
	response.Handoff = protocol.Ptr(draft)
	d.sendToClient(client, response)
}

func (d *Daemon) sendSeedReviewWSResult(
	client *wsClient,
	requestID string,
	operation string,
	run *garden.ReviewRun,
	items []garden.ReviewItem,
	candidateCount int,
	err error,
) {
	response := protocol.SeedReviewResultMessage{
		Event: protocol.EventSeedReviewResult, RequestID: requestID,
		Operation: operation, Success: err == nil, CandidateCount: candidateCount,
	}
	if err != nil {
		response.Error = protocol.Ptr(fmt.Sprintf("seed review %s: %v", operation, err))
	} else if run != nil {
		review := gardenReviewToProtocol(*run, items)
		response.Review = &review
	}
	d.sendToClient(client, response)
}

func (d *Daemon) projectGardenReview(runID string) {
	if d.store == nil || d.wsHub == nil || strings.TrimSpace(runID) == "" {
		return
	}
	run, items, err := d.showGardenReview(runID)
	if err != nil {
		d.logf("garden review: project %s: %v", runID, err)
		return
	}
	d.broadcastMessage(&protocol.GardenReviewUpdatedMessage{
		Event:  protocol.EventGardenReviewUpdated,
		Review: gardenReviewToProtocol(run, items),
	})
}

func (d *Daemon) projectGardenReviewJob(jobID string) {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	job, err := runner.Get(strings.TrimSpace(jobID))
	if err != nil || job == nil || job.Kind != gardenReviewClassifyKind {
		return
	}
	var payload gardenReviewJobPayload
	if err := job.DecodePayload(&payload); err != nil {
		d.logf("garden review: read changed job %s: %v", job.ID, err)
		return
	}
	d.projectGardenReview(payload.RunID)
}
