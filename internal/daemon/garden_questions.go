package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSeedQuestion(conn net.Conn, msg *protocol.SeedQuestionMessage) {
	action, err := garden.ParseQuestionAction(msg.Verb)
	if err != nil {
		d.sendGardenError(conn, strings.TrimSpace(msg.Verb), err)
		return
	}
	result, err := d.applySeedQuestion(msg, action)
	if err != nil {
		d.sendGardenError(conn, string(action), err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedQuestionResult: result})
}

func (d *Daemon) handleSeedQuestionWS(client *wsClient, msg *protocol.SeedQuestionMessage) {
	result := protocol.SeedQuestionResultMessage{
		Event: protocol.EventSeedQuestionResult, RequestID: protocol.Deref(msg.RequestID),
	}
	action, err := garden.ParseQuestionAction(msg.Verb)
	if err == nil {
		result.Result, err = d.applySeedQuestion(msg, action)
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) applySeedQuestion(msg *protocol.SeedQuestionMessage, action garden.QuestionAction) (*protocol.SeedQuestionResult, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	if action == garden.QuestionAsk && !parseBooleanSetting(d.store.GetSetting(SettingGardenNeedsHumanEnabled)) {
		return nil, fmt.Errorf("%s is off; enable %s in attn Settings before asking", SettingGardenNeedsHumanEnabled, SettingGardenNeedsHumanEnabled)
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	memberName := strings.TrimSpace(protocol.Deref(msg.Member))
	actorSession := sessionID
	if memberName != "" {
		actorSession = ""
	}
	actor := garden.Tender{
		Session: actorSession,
		Member:  d.resolveTenderMember(memberName, sessionID),
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(msg.SeedID)
		if err != nil {
			return nil, err
		}
		tender := seed.Tender()
		next, err := garden.ChangeQuestion(
			seed, action, protocol.Deref(msg.Body), actor,
			formatGardenTime(d.gardenTime()), fmt.Sprintf("q-%s-%d", seed.ID, doc.Rev+1),
		)
		if err != nil {
			return nil, err
		}

		var written docstore.Document
		var note *protocol.SeedNote
		if action == garden.QuestionAnswer || action == garden.QuestionDismiss {
			kind := garden.NoteKindAnswer
			label := "Answer"
			if action == garden.QuestionDismiss {
				kind = garden.NoteKindDismiss
				label = "Dismissed"
			}
			entry := garden.Note{
				Seed: seed.ID, Kind: kind,
				Body: fmt.Sprintf("**Question:**\n\n%s\n\n**%s:**\n\n%s",
					seed.Question.Text, label, strings.TrimSpace(protocol.Deref(msg.Body))),
				AuthorSession: actor.Session, AuthorMember: actor.Member,
			}
			if err := garden.ValidateNote(entry.Body); err != nil {
				return nil, fmt.Errorf("the question and %s must fit together on the log: %w", action, err)
			}
			var notes []protocol.SeedNote
			written, notes, err = d.writeSeedMoveWithNotes(
				*schema, next, doc.Rev, FactGardenQuestionChanged, []garden.Note{entry})
			if err == nil {
				note = &notes[0]
			}
		} else {
			written, err = d.writeSeed(*schema, next, doc.Rev, FactGardenQuestionChanged)
		}
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			return nil, err
		}

		if note != nil {
			d.mirrorSeedNoteOntoTicket(sessionID, seed.ID, note.Body)
			if tenderSession, ok := d.liveSessionForTender(tender); ok {
				d.claimAndDeliverSeedBell(tenderSession, seed.ID, string(action))
			}
		}
		return &protocol.SeedQuestionResult{
			Seed: seedToProtocol(next, written, d.gardenReady()[seed.ID]), Note: note,
		}, nil
	}
	return nil, fmt.Errorf(
		"%s was rewritten under all %d attempts to %s its question; read it again with `attn seed show %s` and retry",
		msg.SeedID, attempts, action, msg.SeedID)
}
