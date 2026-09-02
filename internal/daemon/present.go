package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/present"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func presentationToProto(p *store.Presentation) protocol.Presentation {
	out := protocol.Presentation{
		ID:                   p.ID,
		SessionID:            p.SessionID,
		Title:                p.Title,
		Kind:                 p.Kind,
		RepoPath:             p.RepoPath,
		Status:               p.Status,
		CreatedAt:            p.CreatedAt,
		LatestRoundSeq:       p.LatestRoundSeq,
		LatestRoundSubmitted: p.LatestRoundSubmitted,
	}
	if p.TicketID != nil {
		out.TicketID = p.TicketID
	}
	return out
}

func commentToProto(c *store.PresentationComment) protocol.PresentationComment {
	return protocol.PresentationComment{
		ID:        c.ID,
		RoundID:   c.RoundID,
		Filepath:  c.Filepath,
		LineStart: c.LineStart,
		LineEnd:   c.LineEnd,
		Side:      c.Side,
		Content:   c.Content,
		Author:    c.Author,
		CreatedAt: c.CreatedAt,
	}
}

func manifestToView(m *present.Manifest, annotations map[string][]present.ResolvedAnnotation) protocol.PresentManifestView {
	files := make([]protocol.PresentFile, len(m.Files))
	for i, f := range m.Files {
		pf := protocol.PresentFile{Path: f.Path}
		if f.Note != "" {
			pf.Note = protocol.Ptr(f.Note)
		}
		if resolved, ok := annotations[f.Path]; ok {
			pf.Annotations = make([]protocol.PresentAnnotation, len(resolved))
			for j, r := range resolved {
				pf.Annotations[j] = protocol.PresentAnnotation{
					LineStart: r.LineStart,
					LineEnd:   r.LineEnd,
					Comments:  r.Comments,
				}
			}
		}
		files[i] = pf
	}
	skip := m.Skip
	if skip == nil {
		skip = []string{}
	}
	view := protocol.PresentManifestView{
		Title: m.Title,
		Files: files,
		Skip:  skip,
	}
	if m.Summary != "" {
		view.Summary = protocol.Ptr(m.Summary)
	}
	return view
}

func roundToProto(r *store.PresentationRound, repoDir string) (*protocol.PresentationRound, error) {
	m, err := present.ParseManifest([]byte(r.ManifestYAML))
	if err != nil {
		return nil, fmt.Errorf("parse stored manifest for round %s: %w", r.ID, err)
	}
	annotations, _ := present.ResolveAnnotations(m, repoDir, r.HeadSHA)
	out := &protocol.PresentationRound{
		ID:             r.ID,
		PresentationID: r.PresentationID,
		Seq:            r.Seq,
		BaseSHA:        r.BaseSHA,
		HeadSHA:        r.HeadSHA,
		CreatedAt:      r.CreatedAt,
		Manifest:       manifestToView(m, annotations),
	}
	if r.SubmittedAt != nil {
		out.SubmittedAt = r.SubmittedAt
	}
	if r.Verdict != nil {
		out.Verdict = r.Verdict
	}
	return out, nil
}

func formatAnchorIssue(issue present.AnchorIssue) string {
	if issue.Index < 0 {
		return fmt.Sprintf("%s: %s", issue.Path, issue.Message)
	}
	return fmt.Sprintf("%s[%d]: %s", issue.Path, issue.Index, issue.Message)
}

// The daemon is the sole authority for parsing and pinning: never trust a
// caller-supplied SHA or manifest shape.
func (d *Daemon) handlePresentOpen(conn net.Conn, msg *protocol.PresentOpenMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "present open: source_session_id is required")
		return
	}

	m, err := present.ParseManifest([]byte(msg.ManifestYaml))
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}
	baseSHA, headSHA, err := present.Pin(m)
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}

	_, issues := present.ResolveAnnotations(m, m.Frame.Repo, headSHA)
	var warnings []string
	var errMessages []string
	for _, issue := range issues {
		if issue.Warning {
			warnings = append(warnings, formatAnchorIssue(issue))
		} else {
			errMessages = append(errMessages, formatAnchorIssue(issue))
		}
	}
	if len(errMessages) > 0 {
		d.sendError(conn, "present open: annotation errors:\n"+strings.Join(errMessages, "\n"))
		return
	}

	now := time.Now()
	isNewPresentation := false
	var pres *store.Presentation

	if msg.PresentationID != nil && strings.TrimSpace(*msg.PresentationID) != "" {
		presentationID := strings.TrimSpace(*msg.PresentationID)
		existing, err := d.store.GetPresentation(presentationID)
		if err != nil {
			d.sendError(conn, "present open: unknown presentation "+presentationID)
			return
		}
		if existing.SessionID != sourceSessionID {
			d.sendError(conn, "present open: presentation "+presentationID+" does not belong to session "+sourceSessionID)
			return
		}
		pres = existing
	} else {
		var ticketID *string
		if msg.TicketID != nil && strings.TrimSpace(*msg.TicketID) != "" {
			t := strings.TrimSpace(*msg.TicketID)
			ticketID = &t
		} else if ticket, tErr := d.store.ActiveTicketForSession(sourceSessionID); tErr == nil && ticket != nil {
			ticketID = protocol.Ptr(ticket.ID)
		}
		created, err := d.store.CreatePresentation(sourceSessionID, ticketID, m.Title, m.Kind, m.Frame.Repo, now)
		if err != nil {
			d.sendError(conn, "present open: "+err.Error())
			return
		}
		pres = created
		isNewPresentation = true
	}

	round, err := d.store.CreatePresentationRound(pres.ID, msg.ManifestYaml, baseSHA, headSHA, now)
	if err != nil {
		d.sendError(conn, "present open: "+err.Error())
		return
	}

	result := &protocol.PresentOpenResult{
		PresentationID: pres.ID,
		RoundID:        round.ID,
		Seq:            round.Seq,
		BaseSHA:        baseSHA,
		HeadSHA:        headSHA,
		Title:          m.Title,
	}
	if len(warnings) > 0 {
		result.Warnings = warnings
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                true,
		PresentOpenResult: result,
	})

	// Re-fetch so the broadcast carries the fresh latest-round summary.
	if refreshed, err := d.store.GetPresentation(pres.ID); err == nil {
		proto := presentationToProto(refreshed)
		fact := FactPresentationUpdated
		if isNewPresentation {
			fact = FactPresentationAdded
		}
		d.publishFact(fact, proto.ID, nil)
	} else {
		d.logf("present open: failed to refresh presentation %s for broadcast: %v", pres.ID, err)
	}
}

func (d *Daemon) handlePresentFeedback(conn net.Conn, msg *protocol.PresentFeedbackMessage) {
	presentationID := strings.TrimSpace(msg.PresentationID)
	if presentationID == "" {
		d.sendError(conn, "present feedback: presentation_id is required")
		return
	}
	pres, err := d.store.GetPresentation(presentationID)
	if err != nil {
		d.sendError(conn, "present feedback: unknown presentation "+presentationID)
		return
	}

	seq := 0
	if msg.Seq != nil {
		seq = *msg.Seq
	}
	round, err := d.store.GetPresentationRound(presentationID, seq)
	if err != nil {
		d.sendError(conn, "present feedback: "+err.Error())
		return
	}

	comments, err := d.store.ListPresentationComments(round.ID)
	if err != nil {
		d.sendError(conn, "present feedback: "+err.Error())
		return
	}
	feedbackComments := make([]present.FeedbackComment, len(comments))
	for i, c := range comments {
		feedbackComments[i] = present.FeedbackComment{
			Filepath:  c.Filepath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Side:      c.Side,
			Content:   c.Content,
		}
	}

	submittedAt := ""
	if round.SubmittedAt != nil {
		submittedAt = *round.SubmittedAt
	}
	verdict := ""
	if round.Verdict != nil {
		verdict = *round.Verdict
	}
	markdown := present.RenderFeedback(pres.RepoPath, pres.Title, round.Seq, round.BaseSHA, round.HeadSHA, submittedAt, verdict, feedbackComments)
	if pres.Status == "closed" && round.SubmittedAt == nil {
		markdown += "\nPresentation closed without review.\n"
	}

	var verdictPtr *string
	if verdict != "" {
		verdictPtr = &verdict
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		PresentFeedbackResult: &protocol.PresentFeedbackResult{
			Markdown:           markdown,
			Seq:                round.Seq,
			Submitted:          round.SubmittedAt != nil,
			Verdict:            verdictPtr,
			PresentationStatus: pres.Status,
		},
	})
}

func (d *Daemon) handleGetPresentations(client *wsClient, msg *protocol.GetPresentationsMessage) {
	result := protocol.GetPresentationsResultMessage{
		Event:   protocol.EventGetPresentationsResult,
		Success: false,
	}

	list, err := d.store.ListPresentations()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Presentations = make([]protocol.Presentation, len(list))
	for i, p := range list {
		result.Presentations[i] = presentationToProto(p)
	}
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleGetPresentationRound(client *wsClient, msg *protocol.GetPresentationRoundMessage) {
	result := protocol.GetPresentationRoundResultMessage{
		Event:   protocol.EventGetPresentationRoundResult,
		Success: false,
	}

	presentationID := strings.TrimSpace(msg.PresentationID)
	pres, err := d.store.GetPresentation(presentationID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	seq := 0
	if msg.Seq != nil {
		seq = *msg.Seq
	}
	round, err := d.store.GetPresentationRound(presentationID, seq)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	comments, err := d.store.ListPresentationComments(round.ID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	protoRound, err := roundToProto(round, pres.RepoPath)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	protoPres := presentationToProto(pres)
	result.Presentation = &protoPres
	result.Round = protoRound
	result.Comments = make([]protocol.PresentationComment, len(comments))
	for i, c := range comments {
		result.Comments[i] = commentToProto(c)
	}

	// Best-effort drift signal: a rev-parse failure is non-fatal, just omit the
	// field.
	if headSHA, err := attngit.Output(attngit.OpMetadata, pres.RepoPath, "rev-parse", "HEAD"); err == nil {
		result.RepoHeadSHA = protocol.Ptr(strings.TrimSpace(string(headSHA)))
	}

	// A stats lookup failure or empty result must never fail the round fetch.
	stats := d.presentFileStats(pres.RepoPath, round.BaseSHA, round.HeadSHA)
	if len(stats) > 0 {
		for i := range result.Round.Manifest.Files {
			path := result.Round.Manifest.Files[i].Path
			if s, ok := stats[path]; ok {
				result.Round.Manifest.Files[i].Additions = protocol.Ptr(s[0])
				result.Round.Manifest.Files[i].Deletions = protocol.Ptr(s[1])
			}
		}
	}

	// A git error leaves ChangedFiles nil and the round still loads.
	if changed, err := d.presentChangedFiles(pres.RepoPath, round.BaseSHA, round.HeadSHA, stats); err == nil {
		result.Round.ChangedFiles = changed
	}

	result.Success = true
	d.sendToClient(client, result)
}

// Rename lines are skipped: the numstat rename encoding does not cleanly
// resolve to a manifest path. Errors return nil — stats never fail a fetch.
func (d *Daemon) presentFileStats(repoDir, baseSHA, headSHA string) map[string][2]int {
	out, err := attngit.Output(attngit.OpDiff, repoDir, "diff", "--numstat", baseSHA+".."+headSHA)
	if err != nil {
		return nil
	}
	return parsePresentNumstat(string(out))
}

func (d *Daemon) presentChangedFiles(repoDir, baseSHA, headSHA string, stats map[string][2]int) ([]protocol.PresentFile, error) {
	out, err := attngit.Output(attngit.OpDiff, repoDir, "diff", "--name-only", "-z", baseSHA+".."+headSHA)
	if err != nil {
		return nil, err
	}
	var files []protocol.PresentFile
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		pf := protocol.PresentFile{Path: path}
		if s, ok := stats[path]; ok {
			pf.Additions = protocol.Ptr(s[0])
			pf.Deletions = protocol.Ptr(s[1])
		}
		files = append(files, pf)
	}
	return files, nil
}

func parsePresentNumstat(output string) map[string][2]int {
	result := make(map[string][2]int)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "-" || parts[1] == "-" {
			continue
		}
		additions, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		deletions, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		path := parts[2]
		if strings.Contains(path, " => ") {
			continue
		}
		result[path] = [2]int{additions, deletions}
	}
	return result
}

func (d *Daemon) handlePresentSubmitRound(client *wsClient, msg *protocol.PresentSubmitRoundMessage) {
	result := protocol.PresentSubmitRoundResultMessage{
		Event:   protocol.EventPresentSubmitRoundResult,
		RoundID: msg.RoundID,
		Success: false,
	}

	roundID := strings.TrimSpace(msg.RoundID)
	if roundID == "" {
		result.Error = protocol.Ptr("round_id is required")
		d.sendToClient(client, result)
		return
	}

	if msg.Verdict != "approved" && msg.Verdict != "feedback" {
		result.Error = protocol.Ptr(fmt.Sprintf("verdict must be \"approved\" or \"feedback\", got %q", msg.Verdict))
		d.sendToClient(client, result)
		return
	}

	comments := make([]store.PresentationComment, 0, len(msg.Comments))
	for i, c := range msg.Comments {
		if strings.TrimSpace(c.Filepath) == "" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].filepath is required", i))
			d.sendToClient(client, result)
			return
		}
		if c.Side != "new" && c.Side != "old" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].side must be \"new\" or \"old\", got %q", i, c.Side))
			d.sendToClient(client, result)
			return
		}
		if c.LineStart < 1 {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].line_start must be >= 1", i))
			d.sendToClient(client, result)
			return
		}
		if c.LineEnd < c.LineStart {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].line_end must be >= line_start", i))
			d.sendToClient(client, result)
			return
		}
		if strings.TrimSpace(c.Content) == "" {
			result.Error = protocol.Ptr(fmt.Sprintf("comments[%d].content is required", i))
			d.sendToClient(client, result)
			return
		}
		comments = append(comments, store.PresentationComment{
			Filepath:  c.Filepath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Side:      c.Side,
			Content:   c.Content,
			Author:    "user",
		})
	}

	now := time.Now()
	if err := d.store.SubmitPresentationRound(roundID, msg.Verdict, comments, now); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)

	round, err := d.store.GetPresentationRoundByID(roundID)
	if err != nil {
		d.logf("present submit round: failed to reload round %s after submit: %v", roundID, err)
		return
	}
	pres, err := d.store.GetPresentation(round.PresentationID)
	if err != nil {
		d.logf("present submit round: failed to load presentation %s after submit: %v", round.PresentationID, err)
		return
	}

	d.publishFact(FactPresentationUpdated, pres.ID, nil)

	if !msg.Handback {
		return
	}
	d.handbackPresentationRound(pres, round.Seq, msg.Verdict)
}

// A bare session gets a best-effort doorbell, silently skipped while it waits
// for approval — the accepted limitation with no durable inbox.
func (d *Daemon) handbackPresentationRound(pres *store.Presentation, seq int, verdict string) {
	var notice string
	if verdict == "approved" {
		notice = fmt.Sprintf("Present round %d of %q approved — run `attn present feedback %s`", seq, pres.Title, pres.ID)
	} else {
		notice = fmt.Sprintf("Present round %d of %q submitted — run `attn present feedback %s`", seq, pres.Title, pres.ID)
	}

	if pres.TicketID != nil && strings.TrimSpace(*pres.TicketID) != "" {
		ticketID := strings.TrimSpace(*pres.TicketID)
		_, err := d.store.AddTicketComment(ticketID, "attn", notice, time.Now())
		d.afterTicketMutation(ticketID, err)
		if err != nil {
			d.logf("present handback: failed to comment on ticket %s: %v", ticketID, err)
		}
		return
	}

	session := d.store.Get(pres.SessionID)
	if session == nil || !sessionInputPhaseAllows(sessionInputAtTurnBoundary, session.State) {
		d.logf("present handback: session %s is waiting for approval, skipping doorbell for presentation %s", pres.SessionID, pres.ID)
		return
	}
	delivery := maintenanceSessionInput(
		"present-handback",
		fmt.Sprintf("%s/%d", pres.ID, seq),
		pres.SessionID,
		"\U0001F4FD "+notice+".",
		sessionInputAtTurnBoundary,
	)
	delivery.resend = func() { d.resendPresentationHandback(pres.ID, seq, verdict) }
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.err != nil {
		d.logf("present handback: input failed for session %s: %v", pres.SessionID, attempt.err)
		return
	}
	d.sessionInputs().release(pres.SessionID, delivery.id)
}

func (d *Daemon) resendPresentationHandback(presentationID string, seq int, verdict string) {
	pres, err := d.store.GetPresentation(presentationID)
	if err != nil || pres == nil || pres.Status == "closed" {
		return
	}
	d.handbackPresentationRound(pres, seq, verdict)
}

func (d *Daemon) handlePresentClose(client *wsClient, msg *protocol.PresentCloseMessage) {
	result := protocol.PresentCloseResultMessage{
		Event:          protocol.EventPresentCloseResult,
		PresentationID: msg.PresentationID,
		Success:        false,
	}

	presentationID := strings.TrimSpace(msg.PresentationID)
	if presentationID == "" {
		result.Error = protocol.Ptr("presentation_id is required")
		d.sendToClient(client, result)
		return
	}

	if err := d.store.ClosePresentation(presentationID, time.Now()); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Success = true
	d.sendToClient(client, result)

	d.publishFact(FactPresentationUpdated, presentationID, nil)
}

func (d *Daemon) projectPresentation(ev bus.Event) {
	pres, err := d.store.GetPresentation(ev.Subject)
	if err != nil {
		d.logf("present: reload presentation %s for %s: %v", ev.Subject, ev.Name, err)
		return
	}
	proto := presentationToProto(pres)
	if ev.Name == FactPresentationAdded {
		d.broadcastMessage(protocol.PresentationAddedMessage{
			Event:        protocol.EventPresentationAdded,
			Presentation: proto,
		})
		return
	}
	d.broadcastMessage(protocol.PresentationUpdatedMessage{
		Event:        protocol.EventPresentationUpdated,
		Presentation: proto,
	})
}
