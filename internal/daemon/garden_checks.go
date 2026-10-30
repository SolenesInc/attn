package daemon

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

type seedCheckOutcome struct {
	seed           protocol.Seed
	note           protocol.SeedNote
	ringRecipients []string
}

func (d *Daemon) handleSeedCheck(conn net.Conn, msg *protocol.SeedCheckMessage) {
	outcome, err := d.applySeedCheck(msg)
	if err != nil {
		d.sendGardenError(conn, "check", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok: true,
		SeedCheckResult: &protocol.SeedCheckResult{
			Seed: outcome.seed, Note: outcome.note, RingRecipients: outcome.ringRecipients,
		},
	})
}

func (d *Daemon) handleSeedCheckWS(client *wsClient, msg *protocol.SeedCheckMessage) {
	result := protocol.SeedCheckResultMessage{
		Event:     protocol.EventSeedCheckResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	outcome, err := d.applySeedCheck(msg)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Seed = &outcome.seed
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) applySeedCheck(msg *protocol.SeedCheckMessage) (seedCheckOutcome, error) {
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
	actor := garden.Tender{Session: sessionID, Member: d.resolveTenderMember(protocol.Deref(msg.Member), sessionID)}
	move := garden.CheckMove{
		Verb: verb, CheckID: msg.CheckID, Text: protocol.Deref(msg.Text), Shared: protocol.Deref(msg.Shared),
		Summary: protocol.Deref(msg.Summary), Evidence: msg.Evidence, Commit: protocol.Deref(msg.Commit),
		Actor: actor,
	}
	ring := protocol.Deref(msg.Ring)
	var artifacts []string
	if verb == garden.CheckVerbReport && len(move.Evidence) > 0 {
		if artifacts, err = d.seedArtifactNames(seedID); err != nil {
			return seedCheckOutcome{}, err
		}
	}

	const attempts = 3
	for range attempts {
		read, err := d.readGarden(protocol.Deref(msg.ProfileID))
		if err != nil {
			return seedCheckOutcome{}, err
		}
		next, note, err := garden.ApplyCheckMove(read.plots, seedID, move, artifacts, d.gardenTime())
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
		outcome := seedCheckOutcome{seed: read.withSeed(next, written).seed(next, written), note: notes[0]}
		if ring {
			outcome.ringRecipients = recipients
		}
		return outcome, nil
	}
	return seedCheckOutcome{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to move %s; read it again with `attn seed show %s` and decide from what it says now",
		seedID, attempts, msg.CheckID, seedID)
}

func (d *Daemon) refuseRepeatedCheckIDs(checks []garden.Check, plotID, profileID string) error {
	read, err := d.readGarden(profileID)
	if err != nil {
		return err
	}
	if checkID, owner, used := read.plots.UsedCheckID(plotID, checks); used {
		return fmt.Errorf("%s is already used by %s in that plot, and one id names one statement in a plot tree; pick another number", checkID, owner)
	}
	return nil
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

func (d *Daemon) seedArtifactNames(seedID string) ([]string, error) {
	managed, err := d.seedArtifacts(seedID)
	if err != nil {
		return nil, fmt.Errorf("read seed artifacts: %w", err)
	}
	names := make([]string, 0, len(managed))
	for _, artifact := range managed {
		names = append(names, artifact.Filename)
	}
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		return nil, err
	}
	for _, reference := range garden.CurrentArtifacts(notes) {
		names = append(names, reference.Label())
	}
	return names, nil
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
			parsed, err := inbox.ParseAddress(address)
			if err != nil {
				return nil, err
			}
			name := parsed.MemberID()
			if name == "" {
				name = string(parsed.SessionID())
			}
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

var citedCheckID = regexp.MustCompile(`\bC[1-9][0-9]*\b`)

func (d *Daemon) citedChecks(seedID, body string) []string {
	cited := citedCheckID.FindAllString(body, -1)
	if len(cited) == 0 {
		return nil
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil
	}
	read := d.readGardenAround(seed)
	var out []string
	for _, check := range read.plots.EffectiveChecks(seed.ID) {
		if !check.Removed && slices.Contains(cited, check.ID) {
			out = append(out, check.ID)
		}
	}
	return out
}

func checkNoteInboxText(note garden.Note) string {
	by := note.AuthorMember
	if by == "" {
		by = shortSessionID(note.AuthorSession)
	}
	if by == "" {
		by = "the attn app"
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
			Report: checkReportToProtocol(check.Result.Report), Verdict: checkVerdictToProtocol(check.Result.Verdict),
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

func checkReportToProtocol(report *garden.CheckReport) *protocol.SeedCheckReport {
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
		wire.Evidence = append(wire.Evidence, cited)
	}
	if report.Commit != "" {
		wire.Commit = protocol.Ptr(report.Commit)
	}
	if report.BySession != "" {
		wire.BySession = protocol.Ptr(report.BySession)
	}
	if report.ByMember != "" {
		wire.ByMember = protocol.Ptr(report.ByMember)
	}
	return wire
}

func checkVerdictToProtocol(verdict *garden.CheckVerdict) *protocol.SeedCheckVerdict {
	if verdict == nil {
		return nil
	}
	wire := &protocol.SeedCheckVerdict{Confirmed: verdict.Confirmed, At: verdict.At}
	if verdict.Reason != "" {
		wire.Reason = protocol.Ptr(verdict.Reason)
	}
	if verdict.BySession != "" {
		wire.BySession = protocol.Ptr(verdict.BySession)
	}
	if verdict.ByMember != "" {
		wire.ByMember = protocol.Ptr(verdict.ByMember)
	}
	return wire
}

func checkDefinitionsFromProtocol(wire []protocol.SeedCheckDefinition) []garden.Check {
	if len(wire) == 0 {
		return nil
	}
	out := make([]garden.Check, 0, len(wire))
	for _, check := range wire {
		out = append(out, garden.Check{
			ID: strings.TrimSpace(check.ID), Text: strings.TrimSpace(check.Text), Shared: protocol.Deref(check.Shared),
		})
	}
	return out
}
