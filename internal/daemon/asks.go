package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	askRepliesForgetKind     = "ask_replies_forget"
	askRepliesForgetInterval = time.Hour
	askRepliesForgetTimeout  = time.Minute
	askReplyShownFor         = 24 * time.Hour
)

func (d *Daemon) asksSettingOn() bool {
	return d.on(setting.AsksEnabled)
}

func (d *Daemon) launchedWithAsks(sessionID protocol.SessionID) bool {
	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		return intent.Asks
	}
	return d.asksSettingOn()
}

func (d *Daemon) askOpen(msg *protocol.AskOpenMessage) (protocol.Ask, error) {
	session, err := d.askingSession(protocol.TrimID(msg.SessionID))
	if err != nil {
		return protocol.Ask{}, err
	}
	openedAt := d.clock.Now()
	parts, err := ask.Parse(msg.Markdown, openedAt)
	if err != nil {
		return protocol.Ask{}, err
	}
	images, err := parseAskImages(msg.Images)
	if err != nil {
		return protocol.Ask{}, err
	}
	to, err := d.askAddressOf(session, strings.TrimSpace(protocol.Deref(msg.SeedID)))
	if err != nil {
		return protocol.Ask{}, err
	}
	if key, _ := to.Member(); !key.IsZero() {
		if _, retired := d.retiredCrewMember(key.String()); retired {
			return protocol.Ask{}, protocol.Refuse(protocol.RefusalWrongState, "you are retired, so the user takes no new asks from you; file your letter with `{attn handoff}`")
		}
	}
	re, err := d.askThread(session.ID, strings.TrimSpace(protocol.Deref(msg.Re)))
	if err != nil {
		return protocol.Ask{}, err
	}
	id, err := ask.NewID()
	if err != nil {
		return protocol.Ask{}, err
	}
	stored := store.Ask{
		Re:        re,
		ID:        id,
		SessionID: session.ID,
		To:        to,
		Subject:   ask.Subject(protocol.Deref(msg.Subject), parts),
		Markdown:  msg.Markdown,
		BaseDir:   msg.BaseDir,
		CreatedAt: openedAt.UTC().Format(time.RFC3339Nano),
	}
	opened, err := d.describeAsk(stored, parts)
	if err != nil {
		return protocol.Ask{}, err
	}
	if err := writeAskImages(id, images); err != nil {
		return protocol.Ask{}, fmt.Errorf("keep the ask's images: %v", err)
	}
	err = d.store.AddAsk(stored, defaultDueAt(opened.wire))
	if err != nil {
		d.forgetAskImages([]string{id})
	}
	switch {
	case errors.Is(err, store.ErrAskerGone):
		return protocol.Ask{}, errSessionNotLive(session.ID)
	case errors.Is(err, store.ErrAskSeedClosed):
		seedID, _ := to.TenderOf()
		return protocol.Ask{}, fmt.Errorf("%s closed before the ask was saved; ask again", seedID)
	case err != nil:
		return protocol.Ask{}, err
	}
	d.publishFact(FactAskOpened, id, nil)
	return opened.wire, nil
}

func (d *Daemon) askThread(sessionID protocol.SessionID, earlierID string) (*protocol.AskThread, error) {
	if earlierID == "" {
		return nil, nil
	}
	stored, err := d.store.GetAsk(earlierID)
	if errors.Is(err, store.ErrAskGone) {
		return &protocol.AskThread{AskID: earlierID}, nil
	}
	if err != nil {
		return nil, err
	}
	earlier, err := d.readAsk(stored)
	if err != nil {
		return nil, err
	}
	if d.askReach(sessionID, d.askAddressHeldBy(sessionID), earlier) == askOutOfReach {
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "%s is not an ask of this session or of an agent it oversees, so a new ask cannot follow it; ask without naming an earlier ask", earlierID)
	}
	thread := &protocol.AskThread{AskID: earlier.stored.ID, Subject: protocol.Ptr(earlier.stored.Subject)}
	if reply := earlier.wire.Reply; reply != nil {
		answer, err := ask.Answered(earlier.wire)
		if err != nil {
			return nil, err
		}
		thread.Answer, thread.AnsweredAt = protocol.Ptr(answer), protocol.Ptr(reply.AnsweredAt)
	}
	return thread, nil
}

func (d *Daemon) handleAskOpen(conn net.Conn, msg *protocol.AskOpenMessage) {
	opened, err := d.askOpen(msg)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(command.Response(nil, command.AsRefusal(err)))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskOpenResult: &opened})
}

func (d *Daemon) askAmend(msg *protocol.AskAmendMessage) (protocol.Ask, error) {
	shown, _, err := d.reachableOpenAsk(protocol.TrimID(msg.SessionID), msg.AskID)
	if err != nil {
		return protocol.Ask{}, err
	}
	if strings.TrimSpace(msg.Markdown) == "" {
		return protocol.Ask{}, protocol.Refuse(protocol.RefusalInvalidInput, "the amendment has no markdown; write what changed")
	}
	parts, err := ask.Parse(msg.Markdown, d.clock.Now())
	if err != nil {
		return protocol.Ask{}, err
	}
	if len(ask.Questions(parts)) > 0 {
		return protocol.Ask{}, protocol.Refuse(protocol.RefusalInvalidInput, "an amendment adds markdown and cannot hold a question block, so %s keeps the questions the user is answering; to ask something new, open another ask with `{attn ask}`", shown.stored.ID)
	}
	if !slices.Equal(ask.ImagePaths(parts, msg.BaseDir), ask.ImagePaths(parts, shown.stored.BaseDir)) {
		return protocol.Ask{}, protocol.Refuse(protocol.RefusalInvalidInput, "the user's view resolves the ask's images against %s, where %s was opened; name each image of the amendment by its absolute path", shown.stored.BaseDir, shown.stored.ID)
	}
	held := len(shown.stored.Markdown)
	for _, amendment := range shown.stored.Amendments {
		held += len(amendment.Markdown)
	}
	if held+len(msg.Markdown) > ask.MaxMarkdownBytes {
		return protocol.Ask{}, protocol.Refuse(protocol.RefusalInvalidInput, "an ask holds at most %d KiB of markdown, its amendments included; %s holds %d bytes, and this amendment adds %d. Close it and open another ask",
			ask.MaxMarkdownBytes>>10, shown.stored.ID, held, len(msg.Markdown))
	}
	images, err := parseAskImages(msg.Images)
	if err != nil {
		return protocol.Ask{}, err
	}
	if err := refuseImagesThatReplaceSavedOnes(shown.stored.ID, images); err != nil {
		return protocol.Ask{}, err
	}
	if err := refuseImagesPastTheAsksLimit(shown.stored.ID, images); err != nil {
		return protocol.Ask{}, err
	}
	for _, image := range images {
		if err := writeAskImage(shown.stored.ID, image); err != nil {
			return protocol.Ask{}, fmt.Errorf("keep the amendment's images: %v", err)
		}
	}
	err = d.store.AmendAsk(shown.stored.ID, msg.Markdown, d.clock.Now())
	if errors.Is(err, store.ErrAskNotOpen) {
		d.forgetAskImages([]string{shown.stored.ID})
		return protocol.Ask{}, errAskGone
	}
	if err != nil {
		return protocol.Ask{}, err
	}
	d.publishFact(FactAskAmended, shown.stored.ID, nil)
	stored, err := d.store.GetAsk(shown.stored.ID)
	if err != nil {
		return protocol.Ask{}, err
	}
	amended, err := d.readAsk(stored)
	if err != nil {
		return protocol.Ask{}, err
	}
	return amended.wire, nil
}

func (d *Daemon) handleAskAmend(conn net.Conn, msg *protocol.AskAmendMessage) {
	amended, err := d.askAmend(msg)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(command.Response(nil, command.AsRefusal(err)))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskAmendResult: &amended})
}

func (d *Daemon) askAddressOf(session *protocol.Session, ref string) (who.Address, error) {
	seedID := ref
	if ref != "" {
		var err error
		if seedID, err = d.resolveSeedRef(ref, session.ProfileID); err != nil {
			return who.Address{}, err
		}
	}
	if seedID == "" {
		if stake, ok := d.gardenDispatchCrown(session.ID); ok && stake != "" && d.holdsInbox(session.ID, who.ToTenderOf(stake)) {
			return who.ToTenderOf(stake), nil
		}
		return d.inboxAddressOf(session.ID), nil
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return who.Address{}, err
	}
	if seed.ProfileID != session.ProfileID {
		return who.Address{}, d.requireSeedInProfile(seed.ID, session.ProfileID, false)
	}
	to := who.ToTenderOf(seed.ID)
	if d.holdsInbox(session.ID, to) {
		return to, nil
	}
	if !seed.Tender.IsZero() {
		return who.Address{}, fmt.Errorf("%s is being tended by %s, and an ask is about a seed you tend; ask without naming a seed", seed.ID, d.partyName(seed.Tender))
	}
	return who.Address{}, protocol.Refuse(protocol.RefusalWrongState, "nobody is tending %s, and an ask is about a seed you tend; tend it first ({attn seed tend %s}), or ask without naming a seed", seed.ID, seed.ID)
}

func (d *Daemon) holdsInbox(sessionID protocol.SessionID, a who.Address) bool {
	holder := d.inboxHolder(a)
	return holder != nil && holder.ID == sessionID
}

func (d *Daemon) askAddressHeldBy(sessionID protocol.SessionID) func(who.Address) bool {
	roles := d.inboxRoleAddresses(sessionID)
	return func(to who.Address) bool {
		if _, seed := to.TenderOf(); seed {
			return d.holdsInbox(sessionID, to)
		}
		return slices.Contains(roles, to)
	}
}

func (d *Daemon) askingSession(sessionID protocol.SessionID) (*protocol.Session, error) {
	session := d.store.Get(sessionID)
	if sessionID == "" || session == nil {
		return nil, errSessionNotLive(sessionID)
	}
	if !d.launchedWithAsks(sessionID) {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "asks are off for this session because it launched without them; ask in the conversation instead, and run `{attn turn open}` if a notification started your run")
	}
	return session, nil
}

func errSessionNotLive(sessionID protocol.SessionID) error {
	return fmt.Errorf("session %q is not live in attn", sessionID)
}

type askReach int

const (
	askOutOfReach askReach = iota
	askHeld
	askOverseen
)

func (d *Daemon) askReach(sessionID protocol.SessionID, held func(who.Address) bool, a shownAsk) askReach {
	switch {
	case held(a.stored.To):
		return askHeld
	case a.profileID != "" && (gardenStanding{daemon: d, profileID: a.profileID}).Oversees(who.Session(sessionID).Actor(), d.askerOf(a.stored)):
		return askOverseen
	}
	return askOutOfReach
}

func (d *Daemon) askerOf(a store.Ask) who.Party {
	if seedID, ok := a.To.TenderOf(); ok {
		if seed, _, err := d.readSeed(seedID); err == nil {
			return seed.Tender
		}
		return who.Party{}
	}
	if key, ok := a.To.Member(); ok {
		return who.Member(key)
	}
	return who.Session(a.SessionID)
}

func (d *Daemon) askList(sessionID protocol.SessionID) (*protocol.AskListResult, error) {
	if d.store.Get(sessionID) == nil {
		return nil, errSessionNotLive(sessionID)
	}
	result := &protocol.AskListResult{Enabled: d.launchedWithAsks(sessionID), Asks: []protocol.Ask{}, Overseen: []protocol.OverseenAsk{}}
	held := d.askAddressHeldBy(sessionID)
	for _, a := range d.asks() {
		if a.stored.Reply != nil {
			continue
		}
		switch d.askReach(sessionID, held, a) {
		case askHeld:
			result.Asks = append(result.Asks, a.wire)
		case askOverseen:
			result.Overseen = append(result.Overseen, protocol.OverseenAsk{Ask: a.wire, From: d.actorView(d.askerOf(a.stored).Actor())})
		}
	}
	return result, nil
}

func (d *Daemon) reachableOpenAsk(sessionID protocol.SessionID, askID string) (shownAsk, askReach, error) {
	unreachable := fmt.Errorf("%s is not an open ask of this session or of an agent it oversees", askID)
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(askID))
	if err != nil {
		return shownAsk{}, askOutOfReach, unreachable
	}
	shown, err := d.readAsk(stored)
	if err != nil {
		return shownAsk{}, askOutOfReach, err
	}
	reach := d.askReach(sessionID, d.askAddressHeldBy(sessionID), shown)
	if reach == askOutOfReach {
		return shownAsk{}, askOutOfReach, unreachable
	}
	return shown, reach, nil
}

func (d *Daemon) askShow(sessionID protocol.SessionID, askID string) (*protocol.AskShowResult, error) {
	shown, reach, err := d.reachableOpenAsk(sessionID, askID)
	if err != nil {
		return nil, err
	}
	return &protocol.AskShowResult{
		Ask: shown.wire, Markdown: shown.stored.Markdown, From: d.actorView(d.askerOf(shown.stored).Actor()), Held: reach == askHeld,
	}, nil
}

func (d *Daemon) askClose(sessionID protocol.SessionID, askID, reason string) (*protocol.AskCloseResult, error) {
	shown, reach, err := d.reachableOpenAsk(sessionID, askID)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reach == askOverseen && reason == "" {
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "%s is not your ask. Say why you withdraw it, and attn tells its asker: {attn ask close %s -m \"<why>\"}",
			shown.stored.ID, shown.stored.ID)
	}
	if err := d.store.CloseAsk(shown.stored.ID); err != nil {
		return nil, err
	}
	if reach == askOverseen {
		d.tellWithdrawnAsk(sessionID, shown.stored, reason)
	}
	d.announceClosedAsks([]string{shown.stored.ID})
	return &protocol.AskCloseResult{ID: shown.stored.ID}, nil
}

func (d *Daemon) tellWithdrawnAsk(overseer protocol.SessionID, withdrawn store.Ask, reason string) {
	by := d.partyOfSession(overseer)
	text := fmt.Sprintf("%s withdrew the ask %s (%q) from the user, so no answer will come. Why: %s",
		d.partyView(by).Name, withdrawn.ID, withdrawn.Subject, reason)
	if seedID, ok := withdrawn.To.TenderOf(); ok {
		if _, err := d.appendSeedNote(seedID, text, by.Actor(), garden.NoteKindNote, nil, false, overseer); err != nil {
			d.logf("asks: noting the withdrawal of %s on %s: %v", withdrawn.ID, seedID, err)
		}
	}
	now := time.Now()
	message := inbox.Message{ID: uuid.NewString(), Sender: by, Body: text, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
	if err := d.store.PutPeerMessage(message, withdrawn.To); err != nil {
		d.logf("asks: telling %s that %s was withdrawn: %v", withdrawn.To, withdrawn.ID, err)
		return
	}
	d.deliverSavedInbox(withdrawn.To, message.ID, now)
}

func (d *Daemon) seedOpenAsks(seedID string) []protocol.SeedOpenAsk {
	asks, err := d.store.OpenAsksTo(who.ToTenderOf(seedID))
	if err != nil {
		d.logf("asks: reading the open asks about %s: %v", seedID, err)
	}
	open := make([]protocol.SeedOpenAsk, 0, len(asks))
	for _, a := range asks {
		open = append(open, protocol.SeedOpenAsk{ID: a.ID, Subject: a.Subject, CreatedAt: a.CreatedAt})
	}
	return open
}

func (d *Daemon) handleAskAnswer(client *wsClient, msg *protocol.AskAnswerMessage) {
	result := protocol.AskAnswerResultMessage{Event: protocol.EventAskAnswerResult, RequestID: msg.RequestID, AskID: msg.AskID}
	if err := d.answerAsk(msg, client.selectedProfile()); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

var errAskGone = errors.New("this ask was already answered or withdrawn")

func (d *Daemon) answerAsk(msg *protocol.AskAnswerMessage, profileID string) error {
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(msg.AskID))
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	if err != nil {
		return err
	}
	shown, err := d.readAsk(stored)
	if err != nil {
		return err
	}
	if !askShownIn(profileID, shown.profileID) {
		return errAskGone
	}
	to, err := d.answerAddress(profileID, stored, protocol.Deref(msg.ToChief))
	if err != nil {
		return err
	}
	if err := d.commitAnswer(profileID, stored, shown.wire, msg, to); err != nil {
		return err
	}
	d.kickInboxAfterCommit(to)
	d.publishFact(FactAskClosed, stored.ID, nil)
	if holder := d.inboxHolder(stored.To); holder != nil && holder.State != protocol.SessionStatePendingApproval {
		d.handBackTurn(holder.ID)
	}
	return nil
}

func (d *Daemon) commitAnswer(profileID string, stored store.Ask, wire protocol.Ask, msg *protocol.AskAnswerMessage, to who.Address) error {
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	ids := slices.Clone(msg.Attachments)
	for _, answer := range msg.Answers {
		ids = append(ids, answer.Attachments...)
	}
	files, err := d.answerFiles(profileID, stored.ID, ids)
	if err != nil {
		return err
	}
	text, err := ask.Reply(wire, msg.Answers, protocol.Deref(msg.Note), files)
	if err != nil {
		return err
	}
	reply := protocol.AskReply{Answers: msg.Answers, Note: msg.Note, Attachments: files}
	answer := inbox.Item{ID: uuid.NewString(), To: to, Kind: inbox.AskAnswer, Source: stored.ID, Text: d.addressedText(stored, to, text)}
	if seedID, seed := stored.To.TenderOf(); seed {
		err = d.noteAnswer(profileID, stored.ID, seedID, reply, answer, text)
	} else {
		err = d.store.AnswerAsk(profileID, stored.ID, reply, answer, time.Now())
	}
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	if err != nil {
		return fmt.Errorf("save the answer: %w", err)
	}
	return nil
}

func (d *Daemon) handleAskDraftSave(client *wsClient, msg *protocol.AskDraftSaveMessage) {
	result := protocol.AskDraftSaveResultMessage{Event: protocol.EventAskDraftSaveResult, RequestID: msg.RequestID, AskID: msg.AskID, Generation: msg.Draft.Generation}
	var stale *store.StaleAskDraftError
	switch err := d.saveAskDraft(msg, client.selectedProfile()); {
	case errors.As(err, &stale):
		result.Stale, result.Generation = protocol.Ptr(true), stale.Generation
	case err != nil:
		result.Error = protocol.Ptr(err.Error())
	default:
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) saveAskDraft(msg *protocol.AskDraftSaveMessage, profileID string) error {
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(msg.AskID))
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	if err != nil {
		return err
	}
	shown, err := d.readAsk(stored)
	if err != nil {
		return err
	}
	if !askShownIn(profileID, shown.profileID) {
		return errAskGone
	}
	err = d.store.SaveAskDraft(stored.ID, msg.Draft, d.clock.Now())
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	return err
}

func (d *Daemon) draftFiles(profileID, askID string, draft *protocol.AskDraft) []protocol.AskDraftFile {
	owner := protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: askID}
	var files []protocol.AskDraftFile
	ids := slices.Clone(draft.Attachments)
	for _, answer := range draft.Answers {
		ids = append(ids, answer.Attachments...)
	}
	for _, id := range ids {
		a, err := d.store.Attachment(id)
		if err != nil {
			d.logf("asks: reading file %s of the draft of %s: %v", id, askID, err)
			continue
		}
		if a == nil || a.ProfileID != profileID || a.Owner != owner || (a.State != store.AttachmentStaged && a.State != store.AttachmentReady) {
			continue
		}
		files = append(files, protocol.AskDraftFile{Attachment: a.Attachment, Ready: a.State == store.AttachmentReady})
	}
	return files
}

func (d *Daemon) answerFiles(profileID, askID string, ids []string) ([]protocol.Attachment, error) {
	owner := protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: askID}
	files := make([]protocol.Attachment, 0, len(ids))
	for _, id := range ids {
		a, err := d.store.Attachment(id)
		if err != nil {
			return nil, err
		}
		if a == nil || a.ProfileID != profileID || a.Owner != owner {
			return nil, fmt.Errorf("attachment %s was not uploaded for this ask", id)
		}
		if a.State != store.AttachmentReady {
			return nil, fmt.Errorf("attachment %s (%q) is %s, expected ready", id, a.Attachment.Name, a.State)
		}
		files = append(files, a.Attachment)
	}
	return files, nil
}

func (d *Daemon) noteAnswer(profileID, askID, seedID string, reply protocol.AskReply, answer inbox.Item, body string) error {
	note := garden.Note{Seed: seedID, Kind: garden.NoteKindAnswer, Body: body}
	_, _, err := d.mintAndWriteNote(note, false, "", func(write store.DocumentWrite, fact store.BusEvent, events []store.BusEvent, now time.Time) (store.DocumentWriteResult, []int64, error) {
		return d.store.AnswerAskWithNote(profileID, askID, reply, answer, write, fact, events, now)
	})
	return err
}

func (d *Daemon) handleAskFollowUp(client *wsClient, msg *protocol.AskFollowUpMessage) {
	result := protocol.AskFollowUpResultMessage{Event: protocol.EventAskFollowUpResult, RequestID: msg.RequestID, AskID: msg.AskID}
	if holder, err := d.followUpAsk(msg, client.selectedProfile()); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		if holder != nil {
			result.Holder = protocol.Ptr(holder.ID)
		}
	}
	d.sendToClient(client, result)
}

var (
	errAskForgotten = errors.New("attn no longer keeps this ask, so it takes no follow-up")
	errAskStillOpen = errors.New("this ask is still open; answer it instead")
)

func (d *Daemon) followUpAsk(msg *protocol.AskFollowUpMessage, profileID string) (*protocol.Session, error) {
	stored, err := d.store.GetAsk(strings.TrimSpace(msg.AskID))
	if errors.Is(err, store.ErrAskGone) {
		return nil, errAskForgotten
	}
	if err != nil {
		return nil, err
	}
	shown, err := d.readAsk(stored)
	if err != nil {
		return nil, err
	}
	if !askShownIn(profileID, shown.profileID) {
		return nil, errAskForgotten
	}
	if stored.Reply == nil {
		return nil, errAskStillOpen
	}
	to, err := d.answerAddress(profileID, stored, protocol.Deref(msg.ToChief))
	if err != nil {
		return nil, err
	}
	text, err := ask.FollowUp(shown.wire, msg.Text)
	if err != nil {
		return nil, err
	}
	followUp := inbox.Item{ID: uuid.NewString(), To: to, Kind: inbox.AskAnswer, Source: stored.ID, Text: d.addressedText(stored, to, text)}
	if seedID, seed := stored.To.TenderOf(); seed {
		note := garden.Note{Seed: seedID, Kind: garden.NoteKindAnswer, Body: text}
		_, _, err = d.mintAndWriteNote(note, false, "", func(write store.DocumentWrite, fact store.BusEvent, events []store.BusEvent, now time.Time) (store.DocumentWriteResult, []int64, error) {
			return d.store.PutInboxWithNote(followUp, write, fact, events, now)
		})
	} else {
		_, err = d.store.PutInbox(followUp, time.Now())
	}
	if err != nil {
		return nil, fmt.Errorf("save the follow-up: %w", err)
	}
	d.kickInboxAfterCommit(to)
	holder := d.inboxHolder(to)
	if holder != nil && to == stored.To && holder.State != protocol.SessionStatePendingApproval {
		d.handBackTurn(holder.ID)
	}
	return holder, nil
}

func (d *Daemon) answerAddress(profileID string, stored store.Ask, toChief bool) (who.Address, error) {
	if !toChief {
		return stored.To, nil
	}
	if !d.askStranded(stored, "") {
		return who.Address{}, protocol.Refuse(protocol.RefusalWrongState, "%s is not stranded: %s takes the answer, so send it there instead of to the Chief", stored.ID, d.askTaker(stored))
	}
	chief, ok := d.chiefParty(profileID)
	if !ok {
		return who.Address{}, protocol.Refuse(protocol.RefusalWrongState, "this profile has no Chief to take the answer to %s", stored.ID)
	}
	if member, _ := d.chiefMember(profileID); member.Harness() == "" && d.inboxHolder(chief.Address()) == nil {
		return who.Address{}, memberHasNoHarness(member)
	}
	return chief.Address(), nil
}

func (d *Daemon) askTaker(a store.Ask) string {
	if holder := d.inboxHolder(a.To); holder != nil {
		return d.partyView(d.partyOfSession(holder.ID)).Name
	}
	if key, member := a.To.Member(); member {
		return d.crewMemberName(key.String())
	}
	seedID, _ := a.To.TenderOf()
	return fmt.Sprintf("the tender of %s, whom an answer resumes from standby", seedID)
}

func (d *Daemon) askerName(id protocol.SessionID) string {
	if entry := d.store.SessionLedgerEntry(id); d.store.Get(id) == nil && entry != nil && entry.Label != "" {
		return fmt.Sprintf("%s (session %s)", entry.Label, shortSessionID(id))
	}
	return d.partyView(d.partyOfSession(id)).Name
}

func (d *Daemon) addressedText(stored store.Ask, to who.Address, text string) string {
	if to == stored.To {
		return text
	}
	values := prompts.Values{
		"asker":  d.askerName(stored.SessionID),
		"why":    d.whyStranded(stored, ""),
		"answer": text,
	}
	if seedID, seed := stored.To.TenderOf(); seed {
		values["seed_id"] = seedID
		if seed, _, err := d.readSeed(seedID); err == nil {
			values["seed_title"] = strconv.Quote(seed.Title)
		}
	}
	return prompts.RenderText("ask", "to-chief", values)
}

func (d *Daemon) askRepliesForgetHandler(_ context.Context, _ *jobs.Job) (any, error) {
	ids, err := d.store.ForgetRepliesBefore(time.Now().Add(-askReplyShownFor))
	if err != nil {
		return nil, fmt.Errorf("forgetting ask replies: %w", err)
	}
	d.announceClosedAsks(ids)
	return map[string]any{"forgotten": len(ids)}, nil
}

type openAsksError struct {
	seedID string
	then   string
	asks   []store.Ask
}

func (e *openAsksError) Refusal() protocol.Refusal { return protocol.RefusalWrongState }

func (e *openAsksError) Error() string { return protocol.CLIText(e.Mentioned()) }

func (e *openAsksError) Mentioned() string {
	asks, reads := make([]string, len(e.asks)), make([]string, len(e.asks))
	for i, ask := range e.asks {
		asks[i] = fmt.Sprintf("%s (%q)", ask.ID, ask.Subject)
		reads[i] = fmt.Sprintf("`{attn ask show %s}`", ask.ID)
	}
	refusal := fmt.Sprintf("%s has %d open ask(s) to the user: %s. Read what each asks with %s before you choose. If the answer still matters, end your turn, and %s after you act on the answer, which comes to whoever tends the seed. If the answer no longer matters, withdraw the ask with `{attn ask close <id>}`, then %s",
		e.seedID, len(asks), strings.Join(asks, ", "), strings.Join(reads, ", "), e.then, e.then)
	if e.then == string(garden.VerbHarvest) {
		refusal += fmt.Sprintf(". If you do not tend the seed, `{attn seed tend %s}` first", e.seedID)
	}
	return refusal
}

func (d *Daemon) refuseReleaseWithOpenAsks(seedID, then string) error {
	asks, err := d.store.OpenAsksTo(who.ToTenderOf(seedID))
	if err != nil {
		return fmt.Errorf("reading the open asks about %s: %w", seedID, err)
	}
	if len(asks) > 0 {
		return &openAsksError{seedID: seedID, then: then, asks: asks}
	}
	return nil
}

type heldAsksError struct {
	standbySeedID string
	asks          []store.Ask
}

func (e *heldAsksError) Refusal() protocol.Refusal { return protocol.RefusalWrongState }

func (e *heldAsksError) Error() string { return protocol.CLIText(e.Mentioned()) }

func (e *heldAsksError) Mentioned() string {
	asks := make([]string, len(e.asks))
	for i, ask := range e.asks {
		if seedID, ok := ask.To.TenderOf(); ok {
			asks[i] = fmt.Sprintf("%s (%q, about %s)", ask.ID, ask.Subject, seedID)
		} else {
			asks[i] = fmt.Sprintf("%s (%q)", ask.ID, ask.Subject)
		}
	}
	return fmt.Sprintf("this session has %d open ask(s) to the user: %s. A session that waits on the user stays open, so the user can find it: end your turn, and attn types the answer into this conversation. If the user told you to close and the ask is about %s, run `{attn seed standby %s --stay -m \"…\"}`, then `{attn agent close --self -m \"…\"}`",
		len(asks), strings.Join(asks, ", "), e.standbySeedID, e.standbySeedID)
}

func (d *Daemon) refuseClosingStandbyWithHeldAsks(sessionID protocol.SessionID, standbySeedID string) error {
	stored, err := d.store.Asks()
	if err != nil {
		return fmt.Errorf("reading the open asks of %s: %w", sessionID, err)
	}
	held := d.askAddressHeldBy(sessionID)
	var open []store.Ask
	for _, a := range stored {
		if a.Reply == nil && held(a.To) {
			open = append(open, a)
		}
	}
	if len(open) > 0 {
		return &heldAsksError{standbySeedID: standbySeedID, asks: open}
	}
	return nil
}

func (d *Daemon) closeSeedAsks(seed garden.Seed, by who.Actor) []protocol.SeedClosedAsk {
	to := who.ToTenderOf(seed.ID)
	open, err := d.store.OpenAsksTo(to)
	if err != nil {
		d.logf("asks: reading the open asks to %s: %v", to, err)
	}
	ids, err := d.store.CloseAsksTo(to)
	if err != nil {
		d.logf("asks: closing the asks to %s: %v", to, err)
	}
	d.announceClosedAsks(ids)
	var closed []protocol.SeedClosedAsk
	for _, ask := range open {
		closed = append(closed, protocol.SeedClosedAsk{ID: ask.ID, Subject: ask.Subject})
		d.tellAskerOfClosedSeed(ask, seed, by)
	}
	return closed
}

func (d *Daemon) tellAskerOfClosedSeed(withdrawn store.Ask, seed garden.Seed, by who.Actor) {
	if d.store.Get(withdrawn.SessionID) == nil || d.partyOfSession(withdrawn.SessionID).Actor() == by {
		return
	}
	text := fmt.Sprintf("%s %s %s, which withdrew your ask %s (%q) from the user, so no answer will come.",
		d.actorView(by).Name, seed.Status, seed.ID, withdrawn.ID, withdrawn.Subject)
	to := who.ToSession(withdrawn.SessionID)
	if _, err := d.store.PutInbox(inbox.Item{ID: uuid.NewString(), To: to, Kind: inbox.Notice, Text: text}, time.Now()); err != nil {
		d.logf("asks: telling %s that %s was withdrawn: %v", withdrawn.SessionID, withdrawn.ID, err)
		return
	}
	d.kickInbox(to)
}

func (d *Daemon) askStranded(a store.Ask, closing protocol.SessionID) bool {
	return d.whyStranded(a, closing) != ""
}

func (d *Daemon) whyStranded(a store.Ask, closing protocol.SessionID) string {
	if _, member := a.To.Member(); member {
		return ""
	}
	if id, session := a.To.Session(); session {
		if id == closing || d.store.Get(id) == nil {
			return "the session that asked has closed"
		}
		return ""
	}
	seedID, _ := a.To.TenderOf()
	holder, memberID, err := d.inboxRecipient(a.To)
	switch {
	case err != nil:
		return fmt.Sprintf("attn cannot tell who tends %s: %v", seedID, err)
	case memberID != "", holder != nil && holder.ID != closing:
		return ""
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return fmt.Sprintf("attn cannot read %s: %v", seedID, err)
	}
	standby, err := d.seedStandby(seed)
	switch {
	case err != nil:
		return fmt.Sprintf("attn cannot read the standby of %s: %v", seedID, err)
	case standby == nil && seed.Tender.IsZero():
		return fmt.Sprintf("nobody tends %s", seedID)
	case standby == nil:
		return fmt.Sprintf("the session tending %s has closed, and the seed is not on standby", seedID)
	}
	if chosen := d.tenderResumeSetting(seed.ProfileID); chosen != tenderResumeAny {
		return fmt.Sprintf("%s is on standby, but garden.tender_resume=%s does not resume its tender for an answer", seedID, chosen)
	}
	if blocked := d.tenderResumes.blockedReason(seed.ID); blocked != "" {
		return fmt.Sprintf("%s is on standby, but attn cannot resume its tender: %s", seedID, blocked)
	}
	return ""
}

type strandedAsksError struct{ asks []store.Ask }

func (e *strandedAsksError) Refusal() protocol.Refusal { return protocol.RefusalWrongState }

func (e *strandedAsksError) Error() string { return protocol.CLIText(e.Mentioned()) }

func (e *strandedAsksError) Mentioned() string {
	asks := make([]string, len(e.asks))
	for i, ask := range e.asks {
		if seedID, ok := ask.To.TenderOf(); ok {
			asks[i] = fmt.Sprintf("%s (%q, about %s)", ask.ID, ask.Subject, seedID)
		} else {
			asks[i] = fmt.Sprintf("%s (%q)", ask.ID, ask.Subject)
		}
	}
	return fmt.Sprintf("closing this session leaves %d open ask(s) to the user with no agent to take the answer: %s. A session that waits on the user stays open, so the user can find it: end your turn, and attn types the answer into this conversation. Withdraw an ask that no longer matters with `{attn ask close <id>}`",
		len(asks), strings.Join(asks, ", "))
}

func (d *Daemon) refuseStrandingSelfClose(sessionID protocol.SessionID) error {
	stored, err := d.store.Asks()
	if err != nil {
		return fmt.Errorf("reading the open asks of %s: %w", sessionID, err)
	}
	held := d.askAddressHeldBy(sessionID)
	var stranded []store.Ask
	for _, a := range stored {
		if a.Reply == nil && held(a.To) && d.askStranded(a, sessionID) {
			stranded = append(stranded, a)
		}
	}
	if len(stranded) > 0 {
		return &strandedAsksError{asks: stranded}
	}
	return nil
}

var askStrandingFacts = bus.Filter{
	FactSessionRegistered,
	FactSessionReregistered,
	FactSessionClosed,
	FactSessionUnregistered,
	FactDocumentChanged,
	FactSeedStandbyViewChanged,
	FactSettingChanged,
}

func (d *Daemon) mayStrandShownAsks(ev bus.Event) bool {
	var shown bool
	var err error
	switch ev.Name {
	case FactDocumentChanged:
		seedID, seed := strings.CutPrefix(ev.Subject, docstore.Target(garden.Namespace, garden.CollectionSeeds)+"/")
		if !seed {
			return false
		}
		shown, err = d.store.HasAsksTo(who.ToTenderOf(seedID))
	case FactSeedStandbyViewChanged:
		shown, err = d.store.HasAsksTo(who.ToTenderOf(ev.Subject))
	case FactSettingChanged:
		if ev.Subject != setting.GardenTenderResume.Key {
			return false
		}
		shown, err = d.store.HasAsks()
	default:
		shown, err = d.store.HasAsks()
	}
	if err != nil {
		d.logf("asks: reading whether %s changes a shown ask: %v", ev.Name, err)
	}
	return shown
}

func (d *Daemon) announceClosedAsks(ids []string) {
	if len(ids) == 0 {
		return
	}
	d.forgetAskImages(ids)
	d.removeDiscardedAskFiles(ids)
	d.coalesceSnapshots(func() {
		for _, id := range ids {
			d.publishFact(FactAskClosed, id, nil)
		}
	})
}

func (d *Daemon) removeDiscardedAskFiles(ids []string) {
	pausepoint.At(pausepoint.AskFilesDiscarding)
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	discarded, err := d.store.DiscardedAttachmentsOf(protocol.AttachmentOwnerKindAsk, ids)
	if err != nil {
		d.logf("asks: reading the discarded files of %v: %v", ids, err)
		return
	}
	for _, a := range discarded {
		if err := d.removeAttachmentFiles(a); err != nil {
			d.logf("asks: removing the discarded file %s: %v", a.Attachment.ID, err)
		}
	}
}

func (d *Daemon) forgetAskImages(ids []string) {
	for _, id := range ids {
		if err := removeAskImages(id); err != nil {
			d.logf("asks: removing the images of %s: %v", id, err)
		}
	}
}

type shownAsk struct {
	stored    store.Ask
	wire      protocol.Ask
	profileID string
}

func (d *Daemon) asks() []shownAsk {
	stored, err := d.store.Asks()
	if err != nil {
		d.logf("asks: reading asks: %v", err)
		return nil
	}
	asks := make([]shownAsk, 0, len(stored))
	for _, a := range stored {
		shown, err := d.readAsk(a)
		if err != nil {
			d.logf("asks: ask %s cannot be shown: %v", a.ID, err)
			continue
		}
		asks = append(asks, shown)
	}
	return asks
}

func (d *Daemon) readAsk(a store.Ask) (shownAsk, error) {
	openedAt, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	if err != nil {
		return shownAsk{}, fmt.Errorf("ask %s opened at %q: %w", a.ID, a.CreatedAt, err)
	}
	parts, err := ask.Read(a.Markdown, openedAt)
	if err != nil {
		return shownAsk{}, err
	}
	return d.describeAsk(a, parts)
}

func (d *Daemon) describeAsk(a store.Ask, parts []protocol.AskPart) (shownAsk, error) {
	shown := shownAsk{stored: a, wire: protocol.Ask{
		ID:        a.ID,
		SessionID: a.SessionID,
		Subject:   a.Subject,
		Parts:     parts,
		BaseDir:   a.BaseDir,
		CreatedAt: a.CreatedAt,
		Re:        a.Re,
		Reply:     a.Reply,
	}}
	for _, amendment := range a.Amendments {
		shown.wire.Parts = append(shown.wire.Parts, protocol.AskPart{Markdown: protocol.Ptr(amendment.Markdown), AmendedAt: protocol.Ptr(amendment.AmendedAt)})
	}
	if d.askStranded(a, "") {
		shown.wire.Stranded = protocol.Ptr(true)
	}
	seedID, toSeed := a.To.TenderOf()
	memberKey, toMember := a.To.Member()
	switch {
	case toSeed:
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return shownAsk{}, err
		}
		shown.wire.SeedID, shown.wire.SeedTitle = protocol.Ptr(seed.ID), protocol.Ptr(seed.Title)
		if seed.StepSlug != "" {
			shown.wire.SeedSlug = protocol.Ptr(seed.StepSlug)
		}
		shown.profileID = seed.ProfileID
	case toMember:
		shown.wire.CrewMember = protocol.Ptr(memberKey.String())
		shown.profileID = d.crewProfileID(memberKey.String())
	default:
		if session := d.store.Get(a.SessionID); session != nil {
			shown.profileID = session.ProfileID
		}
	}
	if a.Reply != nil {
		files, err := d.store.OwnedAttachments(shown.profileID, protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: a.ID})
		if err != nil {
			return shownAsk{}, err
		}
		if len(files) > 0 {
			reply := *a.Reply
			reply.Attachments = files
			shown.wire.Reply = &reply
		}
	}
	return shown, nil
}

func (d *Daemon) projectAsks() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotAsks, func() {
		asks := d.asks()
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := client.selectedProfile()
			data, ok := snapshots[profileID]
			if !ok {
				var err error
				data, err = json.Marshal(d.asksSnapshot(profileID, asks))
				if err != nil {
					d.logf("asks snapshot: %v", err)
					return
				}
				snapshots[profileID] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}

func (d *Daemon) asksSnapshot(profileID string, asks []shownAsk) *protocol.AsksUpdatedMessage {
	message := &protocol.AsksUpdatedMessage{Event: protocol.EventAsksUpdated, ProfileID: profileID, Asks: []protocol.Ask{}}
	for _, a := range asks {
		if !askShownIn(profileID, a.profileID) {
			continue
		}
		wire := a.wire
		if draft := a.stored.Draft; draft != nil && a.stored.Reply == nil {
			wire.Draft, wire.DraftFiles = draft, d.draftFiles(profileID, a.stored.ID, draft)
		}
		message.Asks = append(message.Asks, wire)
	}
	return message
}

func askShownIn(profileID, askProfileID string) bool {
	return profileID != "" && askProfileID == profileID
}
