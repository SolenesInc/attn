package daemon

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleMarkdownAnnotationsSubmit(client *wsClient, msg *protocol.MarkdownAnnotationsSubmitMessage) {
	targetSession := protocol.SessionID(strings.TrimSpace(protocol.Deref(msg.TargetSessionID)))
	targetSeed := strings.TrimSpace(protocol.Deref(msg.TargetSeedID))
	source, sourceErr := d.resolveAnnotationDocumentSource(msg.DocumentUri, msg.SourceKind, msg.Path, msg.Host, msg.SeedID)
	path, host, seedID := annotationSourcePointers(source)
	result := protocol.MarkdownAnnotationsSubmitResultMessage{
		Event:       protocol.EventMarkdownAnnotationsSubmitResult,
		RequestID:   msg.RequestID,
		DocumentUri: source.documentURI,
		SourceKind:  source.kind,
		Path:        path,
		Host:        host,
		SeedID:      seedID,
		Status:      annotationSubmitStatusError,
	}
	if targetSession != "" {
		result.TargetSessionID = protocol.Ptr(targetSession)
	}
	if targetSeed != "" {
		result.TargetSeedID = protocol.Ptr(targetSeed)
	}
	fail := func(errText string) {
		result.Error = protocol.Ptr(errText)
		d.sendToClient(client, result)
	}
	if sourceErr != nil {
		fail("markdown_annotations_submit: " + sourceErr.Error())
		return
	}
	if source.seedID != "" {
		if err := d.requireSeedInProfile(source.seedID, client.selectedProfile(), false); err != nil {
			fail("markdown_annotations_submit: " + err.Error())
			return
		}
	}
	if (targetSession == "") == (targetSeed == "") {
		fail("markdown_annotations_submit: exactly one of target_session_id or target_seed_id is required")
		return
	}
	if targetSeed != "" && (source.kind != annotationSourceSeed || targetSeed != source.seedID) {
		fail("markdown_annotations_submit: target_seed_id must match the seed document source")
		return
	}
	verdict := protocol.Deref(msg.Verdict)
	asking := len(msg.OnlyIds) > 0
	if verdict != "" || asking {
		if !d.on(setting.CodeReviewEnabled) {
			fail("markdown_annotations_submit: " + reviewOff())
			return
		}
		if errText := reviewSendProblem(source, targetSession, verdict, asking); errText != "" {
			fail("markdown_annotations_submit: " + errText)
			return
		}
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(source.draftKey)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: %v", source.draftKey, err)
		fail(err.Error())
		return
	}
	annotations, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		d.logf("markdown_annotations_submit: %s: corrupt stored draft: %v", source.draftKey, err)
		fail("stored annotation draft is corrupt: " + err.Error())
		return
	}
	whole := annotations
	if asking {
		questions, missing := draftQuestions(annotations, msg.OnlyIds)
		if missing != "" {
			fail("no question " + missing + " in the draft")
			return
		}
		annotations = questions
	}
	if len(annotations) == 0 && verdict == "" {
		fail("no annotations to send")
		return
	}
	orphaned := make(map[string]bool, len(msg.OrphanedIds))
	for _, id := range msg.OrphanedIds {
		orphaned[id] = true
	}
	machine := ""
	if source.host != "" {
		machine = d.outpostName(source.host)
	}
	var payload string
	switch {
	case asking:
		asked := make([]string, 0, len(annotations))
		for _, question := range annotations {
			asked = append(asked, formatQuestionPayload(source, machine, question))
		}
		payload = strings.Join(asked, "\n\n")
	case verdict != "":
		payload = formatReviewPayload(source, machine, verdict, annotations, orphaned)
	default:
		payload = formatMarkdownAnnotationPayload(source, machine, annotations, orphaned)
	}
	if targetSession != "" {
		session := d.store.Get(targetSession)
		if session == nil {
			fail(string("session not found: " + targetSession))
			return
		}
		if !sessionmonitor.PhaseAllows(sessionmonitor.AtTurnBoundary, session.State) {
			result.Status = annotationSubmitStatusSkipped
			d.sendToClient(client, result)
			return
		}
		delivery := sessionmonitor.ComposedInput(msg.RequestID, payload)
		attempt := d.placeDelivery(context.Background(), targetSession, delivery)
		if attempt.Stage != sessionmonitor.Placed {
			if sessionmonitor.DeferredError(attempt.Err) {
				result.Status = annotationSubmitStatusSkipped
				d.sendToClient(client, result)
				return
			}
			d.logf("markdown_annotations_submit: %s -> %s: delivery failed: %v", source.draftKey, targetSession, attempt.Err)
			fail(attempt.Err.Error())
			return
		}
		result.Status = annotationSubmitStatusDelivered
	} else {
		if _, err := d.appendSeedNote(targetSeed, payload, who.User(), "", nil, false, ""); err != nil {
			d.logf("markdown_annotations_submit: %s -> seed %s: note failed: %v", source.draftKey, targetSeed, err)
			fail(err.Error())
			return
		}
		result.Status = annotationSubmitStatusNoted
	}
	result.Success = true
	if asking {
		result.Annotations = whole
		result.Generation = protocol.Ptr(draft.Generation)
		d.sendToClient(client, result)
		return
	}
	remaining, generation, err := d.store.SpendMarkdownAnnotations(source.draftKey, annotations, time.Now())
	if err != nil {
		verb := result.Status
		d.logf("markdown_annotations_submit: %s: %s but clearing drafts failed: %v", source.draftKey, verb, err)
		result.Error = protocol.Ptr(verb + "; failed to clear drafts: " + err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Annotations = remaining
	result.Generation = protocol.Ptr(generation)
	if verdict != "" {
		sent := protocol.ReviewSentMessage{Cmd: protocol.CmdReviewSent, Document: source.path}
		if source.host != "" {
			sent.Host = protocol.Ptr(source.host)
		}
		if _, err := Call[struct{}](d.life.Context(), d.router(), sent); err != nil {
			d.logf("markdown_annotations_submit: %s: delivered but recording the sent review failed: %v", source.draftKey, err)
			result.Error = protocol.Ptr("delivered; the next review start cannot show what changed since: " + d.tileDocumentError(source.host, err).Error())
		}
	}
	d.sendToClient(client, result)
}

func reviewSendProblem(source annotationDocumentSource, targetSession protocol.SessionID, verdict string, asking bool) string {
	switch {
	case verdict != "" && asking:
		return "a question is asked on its own; send only_ids or verdict, not both"
	case source.kind != annotationSourceFile:
		return "only a review document takes a question or a verdict"
	case targetSession == "":
		return "a question or a review goes to a session; set target_session_id"
	case verdict != "" && verdict != protocol.ReviewVerdictApprove && verdict != protocol.ReviewVerdictRequestChanges && verdict != protocol.ReviewVerdictComment:
		return "verdict " + verdict + " is not a verdict. Use approve, request_changes or comment"
	}
	return ""
}

func draftQuestions(annotations []protocol.MarkdownAnnotation, ids []string) ([]protocol.MarkdownAnnotation, string) {
	questions := make([]protocol.MarkdownAnnotation, 0, len(ids))
	for _, id := range ids {
		at := slices.IndexFunc(annotations, func(mark protocol.MarkdownAnnotation) bool {
			return mark.ID == id && mark.Type == protocol.MarkdownAnnotationQuestion
		})
		if at < 0 {
			return nil, id
		}
		questions = append(questions, annotations[at])
	}
	return questions, ""
}
