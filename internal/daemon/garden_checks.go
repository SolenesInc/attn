package daemon

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type seedCheckOutcome struct {
	seed           protocol.Seed
	checkID        string
	note           protocol.SeedNote
	ringRecipients []string
	attached       []protocol.SeedCheckAttachment
}

func (d *Daemon) handleSeedCheck(ctx context.Context, conn net.Conn, msg *protocol.SeedCheckMessage) {
	ctx, cancel := d.connectionContext(ctx, conn)
	defer cancel()
	outcome, err := d.applySeedCheck(ctx, msg)
	if err != nil {
		d.sendGardenError(conn, "check", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true,
		SeedCheckResult: &protocol.SeedCheckResult{
			Seed: outcome.seed, CheckID: outcome.checkID, Note: outcome.note, RingRecipients: outcome.ringRecipients, Attached: outcome.attached,
		},
	})
}

func (d *Daemon) handleSeedCheckWS(client *wsClient, msg *protocol.SeedCheckMessage) {
	result := protocol.SeedCheckResultMessage{
		Event:     protocol.EventSeedCheckResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	outcome, err := d.applySeedCheck(d.life.Context(), msg)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Seed = &outcome.seed
	result.CheckID = protocol.Ptr(outcome.checkID)
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) applySeedCheck(ctx context.Context, msg *protocol.SeedCheckMessage) (seedCheckOutcome, error) {
	verb, err := garden.ParseCheckVerb(msg.Verb)
	if err != nil {
		return seedCheckOutcome{}, err
	}
	seedID := strings.TrimSpace(msg.SeedID)
	if err := garden.ValidateID(seedID); err != nil {
		return seedCheckOutcome{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return seedCheckOutcome{}, err
	}
	sessionID := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	actor := d.actorOfSession(sessionID)
	move := garden.CheckMove{
		Verb: verb, CheckID: strings.TrimSpace(protocol.Deref(msg.CheckID)), Text: protocol.Deref(msg.Text), Shared: protocol.Deref(msg.Shared),
		Summary: protocol.Deref(msg.Summary), Evidence: msg.Evidence, Commit: protocol.Deref(msg.Commit),
		Actor: actor,
	}
	ring := protocol.Deref(msg.Ring)
	var artifacts []garden.CheckEvidence
	var attached []protocol.SeedCheckAttachment
	if verb == garden.CheckVerbReport && len(move.Evidence)+len(msg.EvidenceFiles) > 0 {
		if artifacts, attached, err = d.attachReportEvidence(ctx, msg, &move); err != nil {
			return seedCheckOutcome{}, err
		}
	}

	if verb == garden.CheckVerbAdd {
		d.checkIDsMu.Lock()
		defer d.checkIDsMu.Unlock()
	}
	const attempts = 3
	for range attempts {
		read, err := d.readGarden(protocol.Deref(msg.ProfileID))
		if err != nil {
			return seedCheckOutcome{}, err
		}
		attempt := move
		if verb == garden.CheckVerbAdd && attempt.CheckID == "" {
			attempt.CheckID = read.plots.NextCheckID(seedID)
		}
		next, note, err := garden.ApplyCheckMove(read.plots, seedID, attempt, artifacts, d.gardenTime())
		if err != nil {
			return seedCheckOutcome{}, err
		}
		d.lockGardenRoles()
		var recipients []string
		if ring {
			recipients, err = d.seedParties(seedID, sessionID)
		}
		var written docstore.Document
		var notes []protocol.SeedNote
		if err == nil {
			written, notes, err = d.writeSeedChange(*schema, next, read.docs[seedID].Rev, seedChange{entries: []seedMoveNote{{Note: note, AttentionRequested: ring, CausedBySessionID: sessionID}}})
		}
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return seedCheckOutcome{}, err
		}
		outcome := seedCheckOutcome{seed: read.withSeed(next, written).seed(next, written), checkID: attempt.CheckID, note: notes[0], attached: attached}
		if ring {
			outcome.ringRecipients = recipients
		}
		return outcome, nil
	}
	return seedCheckOutcome{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s a check; read it again with `attn seed show %s` and decide from what it says now",
		seedID, attempts, verb, seedID)
}

func (d *Daemon) attachReportEvidence(ctx context.Context, msg *protocol.SeedCheckMessage, move *garden.CheckMove) ([]garden.CheckEvidence, []protocol.SeedCheckAttachment, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	citable, err := d.citableSeedArtifacts(seedID)
	if err != nil || len(msg.EvidenceFiles) == 0 {
		return citable, nil, err
	}
	dir, err := d.callerSeedArtifactDir(ctx, seedID)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(msg.EvidenceFiles)+len(move.Evidence))
	landed := slices.Clone(citable)
	var copies []string
	for _, path := range msg.EvidenceFiles {
		name := filepath.Base(path)
		names = append(names, name)
		if filepath.Dir(filepath.Clean(path)) != dir {
			copies = append(copies, path)
			landed = append(landed, garden.CheckEvidence{Artifact: name})
		}
	}
	move.Evidence = append(names, move.Evidence...)
	if len(copies) == 0 {
		return citable, nil, nil
	}
	read, err := d.readGarden(protocol.Deref(msg.ProfileID))
	if err != nil {
		return nil, nil, err
	}
	if _, _, err := garden.ApplyCheckMove(read.plots, seedID, *move, landed, d.gardenTime()); err != nil {
		return nil, nil, err
	}
	attached := make([]protocol.SeedCheckAttachment, 0, len(copies))
	for _, path := range copies {
		copied, err := d.submitSeedArtifactTransfer(ctx, &protocol.SeedArtifactTransferMessage{
			Cmd: protocol.CmdSeedArtifactTransfer, ProfileID: msg.ProfileID, SeedID: seedID, Operation: "copy",
			SourcePath: protocol.Ptr(path), SourceSessionID: msg.SourceSessionID,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("copy %s onto %s: %w; nothing was reported, and the same command run again copies what is left and records the report", path, seedID, err)
		}
		attached = append(attached, protocol.SeedCheckAttachment{Filename: filepath.Base(copied.DestinationPath), Replaced: protocol.Deref(copied.Replaced)})
	}
	if citable, err = d.citableSeedArtifacts(seedID); err != nil {
		return nil, nil, err
	}
	return citable, attached, nil
}

func (g gardenRead) withSeed(next garden.Seed, doc docstore.Document) gardenRead {
	seeds := slices.Clone(g.seeds)
	for i := range seeds {
		if seeds[i].ID == next.ID {
			seeds[i] = next
		}
	}
	out := g
	out.seeds = seeds
	out.docs = make(map[string]docstore.Document, len(g.docs))
	for id, d := range g.docs {
		out.docs[id] = d
	}
	out.docs[next.ID] = doc
	out.plots = garden.NewPlotTree(seeds)
	return out
}

func (d *Daemon) seedCheckIDs(profileID, seedID string) (string, error) {
	if seedID == "" {
		return "", nil
	}
	read, err := d.readGarden(profileID)
	if err != nil {
		return "", fmt.Errorf("read the checks of %s: %w", seedID, err)
	}
	var own, shared []string
	for _, check := range read.plots.EffectiveChecks(seedID) {
		switch {
		case check.Removed:
		case check.From != "":
			shared = append(shared, check.ID)
		default:
			own = append(own, check.ID)
		}
	}
	if len(shared) > 0 {
		own = append(own, "the plot's shared "+joinAnd(shared))
	}
	return joinAnd(own), nil
}

func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func (d *Daemon) citableSeedArtifacts(seedID string) ([]garden.CheckEvidence, error) {
	managed, err := d.seedArtifacts(seedID)
	if err != nil {
		return nil, fmt.Errorf("read seed artifacts: %w", err)
	}
	citable := make([]garden.CheckEvidence, 0, len(managed))
	for _, artifact := range managed {
		citable = append(citable, garden.CheckEvidence{Artifact: artifact.Filename, Size: int64(artifact.Size), ModifiedAt: artifact.ModifiedAt})
	}
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		return nil, err
	}
	for _, reference := range garden.CurrentArtifacts(notes) {
		citable = append(citable, garden.CheckEvidence{Artifact: reference.Label()})
	}
	return citable, nil
}

func markEvidenceChangedSinceReport(checks []protocol.SeedCheck, artifacts []protocol.SeedArtifact) {
	current := make(map[string]protocol.SeedArtifact, len(artifacts))
	for _, artifact := range artifacts {
		current[artifact.Filename] = artifact
	}
	for _, check := range checks {
		if check.Report == nil {
			continue
		}
		for i, evidence := range check.Report.Evidence {
			if evidence.ModifiedAt == nil {
				continue
			}
			artifact, present := current[protocol.Deref(evidence.Artifact)]
			changed := !present || artifact.Size != protocol.Deref(evidence.Size) || artifact.ModifiedAt != *evidence.ModifiedAt
			if changed {
				check.Report.Evidence[i].ChangedSinceReport = protocol.Ptr(true)
			}
		}
	}
}

const ringAudienceProbeNoteID = "n-probe"

func (d *Daemon) seedParties(seedID string, cause protocol.SessionID) ([]string, error) {
	occurrence, err := seedEvents.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.NoteAdded, seedID, seedEvents.NoteAddedPayload{
		NoteID: ringAudienceProbeNoteID, AttentionRequested: true, CausedBySessionID: cause,
	})
	if err != nil {
		return nil, err
	}
	return d.seedOccurrenceRecipients(occurrence)
}

func (d *Daemon) seedOccurrenceRecipients(occurrences ...seedEvents.Occurrence) ([]string, error) {
	var out []string
	for _, occurrence := range occurrences {
		event, err := gardenSeedEventModel.Encode(occurrence)
		if err != nil {
			return nil, err
		}
		recipients, err := d.gardenSeedEventRecipients(event.Name, event.Subject, event.Payload)
		if err != nil {
			return nil, err
		}
		for _, address := range recipients.addresses {
			parsed, err := who.ParseAddress(address)
			if err != nil {
				return nil, err
			}
			var name string
			if key, member := parsed.Member(); member {
				name = key.String()
			} else if id, session := parsed.Session(); session {
				name = string(id)
			}
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

func (d *Daemon) checkNoteInboxText(note garden.Note) string {
	by := d.actorName(note.Author)
	if p, ok := note.Author.Party(); ok {
		if id, session := p.Session(); session {
			by = shortSessionID(id)
		}
	}
	return fmt.Sprintf("check %s by %s", garden.CheckNoteHeadline(note.Body), by)
}

func (g gardenRead) checks(id string) []protocol.SeedCheck {
	effective := g.plots.EffectiveChecks(id)
	if len(effective) == 0 {
		return nil
	}
	out := make([]protocol.SeedCheck, 0, len(effective))
	for _, check := range effective {
		wire := protocol.SeedCheck{
			ID: check.ID, Text: check.Text, Removed: check.Removed, State: string(check.Result.State()),
			Report: g.daemon.checkReportToProtocol(check.Result.Report), Verdict: g.daemon.checkVerdictToProtocol(check.Result.Verdict),
		}
		if check.From != "" {
			wire.From = protocol.Ptr(check.From)
		}
		out = append(out, wire)
	}
	return out
}

func (g gardenRead) checkTally(id string) *protocol.SeedCheckTally {
	return checkTallyToProtocol(g.plots.CheckTally(id))
}

func checkTallyToProtocol(tally garden.CheckTally) *protocol.SeedCheckTally {
	if tally.Total == 0 {
		return nil
	}
	return &protocol.SeedCheckTally{Total: tally.Total, Reported: tally.Reported, Confirmed: tally.Confirmed, Rejected: tally.Rejected}
}

func (g gardenRead) sharedChecks(id string) []protocol.SeedSharedCheck {
	rollups := g.plots.SharedCheckRollups(id)
	if len(rollups) == 0 {
		return nil
	}
	out := make([]protocol.SeedSharedCheck, 0, len(rollups))
	for _, rollup := range rollups {
		wire := protocol.SeedSharedCheck{
			ID: rollup.ID, Text: rollup.Text, Removed: rollup.Removed,
			Seeds: make([]protocol.SeedCheckRollupEntry, 0, len(rollup.Seeds)),
		}
		for _, entry := range rollup.Seeds {
			wire.Seeds = append(wire.Seeds, protocol.SeedCheckRollupEntry{
				SeedID: entry.SeedID, State: string(entry.State), HasReport: entry.HasReport,
			})
		}
		out = append(out, wire)
	}
	return out
}

func (d *Daemon) checkReportToProtocol(report *garden.CheckReport) *protocol.SeedCheckReport {
	if report == nil {
		return nil
	}
	wire := &protocol.SeedCheckReport{
		Summary: report.Summary, At: report.At,
		Evidence: make([]protocol.SeedCheckEvidence, 0, len(report.Evidence)),
	}
	for _, evidence := range report.Evidence {
		cited := protocol.SeedCheckEvidence{}
		if evidence.Artifact != "" {
			cited.Artifact = protocol.Ptr(evidence.Artifact)
		}
		if evidence.URL != "" {
			cited.URL = protocol.Ptr(evidence.URL)
		}
		if evidence.ModifiedAt != "" {
			cited.Size = protocol.Ptr(int(evidence.Size))
			cited.ModifiedAt = protocol.Ptr(evidence.ModifiedAt)
		}
		wire.Evidence = append(wire.Evidence, cited)
	}
	if report.Commit != "" {
		wire.Commit = protocol.Ptr(report.Commit)
	}
	if report.Detail {
		wire.Detail = protocol.Ptr(true)
	}
	wire.By = d.actorView(report.By)
	return wire
}

func (d *Daemon) checkVerdictToProtocol(verdict *garden.CheckVerdict) *protocol.SeedCheckVerdict {
	if verdict == nil {
		return nil
	}
	wire := &protocol.SeedCheckVerdict{Confirmed: verdict.Confirmed, At: verdict.At}
	if verdict.Reason != "" {
		wire.Reason = protocol.Ptr(verdict.Reason)
	}
	wire.By = d.actorView(verdict.By)
	return wire
}

func checkDefinitionsFromProtocol(wire []protocol.SeedCheckDefinition) []garden.Check {
	if len(wire) == 0 {
		return nil
	}
	out := make([]garden.Check, 0, len(wire))
	for _, check := range wire {
		out = append(out, garden.Check{
			ID: strings.ToUpper(strings.TrimSpace(protocol.Deref(check.ID))), Text: strings.TrimSpace(check.Text), Shared: protocol.Deref(check.Shared),
		})
	}
	return out
}
