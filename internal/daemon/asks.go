package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) asksSettingOn() bool {
	return parseBooleanSetting(d.daemonSetting(settingAsksEnabled))
}

func (d *Daemon) launchedWithAsks(sessionID protocol.SessionID) bool {
	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		return intent.Asks
	}
	return d.asksSettingOn()
}

func (d *Daemon) handleAskOpen(conn net.Conn, msg *protocol.AskOpenMessage) {
	session, err := d.askingSession(protocol.TrimID(msg.SessionID))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	parts, err := ask.Parse(msg.Markdown)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	to, err := d.askAddressOf(session, strings.TrimSpace(protocol.Deref(msg.SeedID)))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	id, err := ask.NewID()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	stored := store.Ask{
		ID:        id,
		SessionID: session.ID,
		To:        to,
		Subject:   ask.Subject(protocol.Deref(msg.Subject), parts),
		Markdown:  msg.Markdown,
		BaseDir:   strings.TrimSpace(msg.BaseDir),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	opened, err := d.describeAsk(stored, parts)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	switch err := d.store.AddAsk(stored); {
	case errors.Is(err, store.ErrAskerGone):
		d.sendError(conn, errSessionNotLive(session.ID).Error())
		return
	case errors.Is(err, store.ErrAskSeedClosed):
		d.sendError(conn, fmt.Sprintf("%s closed before the ask was saved; ask again", seedAddressID(to)))
		return
	case errors.Is(err, store.ErrAskerRetired):
		d.sendError(conn, "you are retired, so your asks are closed and new ones are refused")
		return
	case err != nil:
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskOpenResult: &opened.wire})
	d.publishFact(FactAskOpened, id, nil)
}

func (d *Daemon) askAddressOf(session *protocol.Session, seedID string) (who.Address, error) {
	b, err := d.bindings()
	if err != nil {
		return who.Address{}, err
	}
	stake := d.openStake(session.ID)
	if seedID == "" || seedID == stake {
		if stake == "" {
			party, ok := b.PartyOf(session.ID)
			if !ok {
				return who.Address{}, errSessionNotLive(session.ID)
			}
			return party.Address(), nil
		}
		return who.ToTenderOf(stake), nil
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return who.Address{}, err
	}
	if seed.ProfileID != session.ProfileID {
		return who.Address{}, d.requireSeedInProfile(seed.ID, session.ProfileID, false)
	}
	to := who.ToTenderOf(seed.ID)
	if holder := d.inboxHolder(to); holder != nil && holder.ID == session.ID {
		return to, nil
	}
	if tender, ok := seed.Claim.Tender(); ok {
		return who.Address{}, fmt.Errorf("%s is being tended by %s, and an ask is about a seed you tend; ask without --seed", seed.ID, d.partyView(tender, b).Name)
	}
	return who.Address{}, fmt.Errorf("nobody is tending %s, and an ask is about a seed you tend; tend it first (attn seed tend %s), or ask without --seed", seed.ID, seed.ID)
}

func (d *Daemon) openStake(sessionID protocol.SessionID) string {
	stake, ok := d.gardenDispatchCrown(sessionID)
	if !ok {
		return ""
	}
	if seed, _, err := d.readSeed(stake); err != nil || garden.Closed(seed.Status) {
		return ""
	}
	return stake
}

func (d *Daemon) askAddressHeldBy(sessionID protocol.SessionID) func(who.Address) bool {
	var roles []who.Address
	if b, err := d.bindings(); err != nil {
		d.logf("asks: reading bindings for %s: %v", sessionID, err)
	} else {
		roles = b.AddressesOf(sessionID)
	}
	stake, _ := d.gardenDispatchCrown(sessionID)
	return func(to who.Address) bool {
		switch seedID := seedAddressID(to); seedID {
		case "":
			return slices.Contains(roles, to)
		case stake:
			return true
		default:
			holder := d.inboxHolder(to)
			return holder != nil && holder.ID == sessionID
		}
	}
}

func (d *Daemon) askingSession(sessionID protocol.SessionID) (*protocol.Session, error) {
	session := d.store.Get(sessionID)
	if sessionID == "" || session == nil {
		return nil, errSessionNotLive(sessionID)
	}
	if !d.launchedWithAsks(sessionID) {
		return nil, errors.New("asks are off for this session because it launched without them; ask in the conversation instead")
	}
	return session, nil
}

func errSessionNotLive(sessionID protocol.SessionID) error {
	return fmt.Errorf("session %q is not live in attn", sessionID)
}

func (d *Daemon) handleAskList(conn net.Conn, msg *protocol.AskListMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		d.sendError(conn, errSessionNotLive(sessionID).Error())
		return
	}
	result := &protocol.AskListResult{Enabled: d.launchedWithAsks(sessionID), Asks: []protocol.Ask{}}
	held := d.askAddressHeldBy(sessionID)
	for _, open := range d.openAsks() {
		if held(open.stored.To) {
			result.Asks = append(result.Asks, open.wire)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskListResult: result})
}

func (d *Daemon) handleAskClose(conn net.Conn, msg *protocol.AskCloseMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	stored, err := d.store.GetOpenAsk(strings.TrimSpace(msg.AskID))
	if err != nil || !d.askAddressHeldBy(sessionID)(stored.To) {
		d.sendError(conn, fmt.Sprintf("%s is not an open ask of this session", msg.AskID))
		return
	}
	if err := d.store.CloseAsk(stored.ID); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
	d.publishFact(FactAskClosed, stored.ID, nil)
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
	open, err := d.readAsk(stored)
	if err != nil {
		return err
	}
	if !askShownIn(profileID, open.profileID) {
		return errAskGone
	}
	text, err := ask.Reply(open.wire, msg.Answers, protocol.Deref(msg.Note))
	if err != nil {
		return err
	}
	if seedID := seedAddressID(stored.To); seedID != "" {
		err = d.noteAnswer(stored.ID, seedID, text)
	} else {
		err = d.inboxAnswer(stored, text)
	}
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	if err != nil {
		return fmt.Errorf("save the answer: %w", err)
	}
	d.publishFact(FactAskClosed, stored.ID, nil)
	if holder := d.inboxHolder(stored.To); holder != nil && holder.State != protocol.SessionStatePendingApproval {
		d.handleSettleTurn(&protocol.SettleTurnMessage{SessionID: holder.ID})
	}
	return nil
}

func (d *Daemon) noteAnswer(askID, seedID, text string) error {
	schema, err := d.notesCollection()
	if err != nil {
		return err
	}
	note := garden.Note{Seed: seedID, Kind: garden.NoteKindNote, Body: text, Author: who.User()}
	_, _, err = d.mintAndWriteNote(*schema, note, true, func(write store.DocumentWrite, fact store.BusEvent, events []store.BusEvent, now time.Time) (store.DocumentWriteResult, []int64, error) {
		return d.store.AnswerAskWithNote(askID, write, fact, events, now)
	})
	return err
}

func (d *Daemon) inboxAnswer(stored store.Ask, text string) error {
	answer := inbox.Item{ID: uuid.NewString(), To: stored.To, Kind: inbox.Notice, Source: stored.ID, Text: text}
	if err := d.store.AnswerAsk(stored.ID, answer, time.Now()); err != nil {
		return err
	}
	d.kickInboxAfterCommit(stored.To)
	return nil
}

func (d *Daemon) closeAsksTo(to who.Address) {
	ids, err := d.store.CloseAsksTo(to)
	if err != nil {
		d.logf("asks: closing the asks to %s: %v", to, err)
	}
	d.announceClosedAsks(ids)
}

func (d *Daemon) announceClosedAsks(ids []string) {
	if len(ids) == 0 {
		return
	}
	d.coalesceSnapshots(func() {
		for _, id := range ids {
			d.publishFact(FactAskClosed, id, nil)
		}
	})
}

type openAsk struct {
	stored    store.Ask
	wire      protocol.Ask
	profileID string
}

func (d *Daemon) openAsks() []openAsk {
	stored, err := d.store.OpenAsks()
	if err != nil {
		d.logf("asks: reading open asks: %v", err)
		return nil
	}
	asks := make([]openAsk, 0, len(stored))
	for _, a := range stored {
		open, err := d.readAsk(a)
		if err != nil {
			d.logf("asks: ask %s cannot be shown: %v", a.ID, err)
			continue
		}
		asks = append(asks, open)
	}
	return asks
}

func (d *Daemon) readAsk(a store.Ask) (openAsk, error) {
	parts, err := ask.Parse(a.Markdown)
	if err != nil {
		return openAsk{}, err
	}
	return d.describeAsk(a, parts)
}

func (d *Daemon) describeAsk(a store.Ask, parts []protocol.AskPart) (openAsk, error) {
	open := openAsk{stored: a, wire: protocol.Ask{
		ID:        a.ID,
		SessionID: a.SessionID,
		Subject:   a.Subject,
		Parts:     parts,
		BaseDir:   a.BaseDir,
		CreatedAt: a.CreatedAt,
	}}
	_, err := who.SwitchAddress(a.To, func(protocol.SessionID) (struct{}, error) {
		if session := d.store.Get(a.SessionID); session != nil {
			open.profileID = session.ProfileID
		}
		return struct{}{}, nil
	}, func(key who.MemberKey) (struct{}, error) {
		open.wire.CrewMember = protocol.Ptr(key.String())
		open.profileID = d.crewProfileID(key.String())
		return struct{}{}, nil
	}, func(seedID string) (struct{}, error) {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return struct{}{}, err
		}
		open.wire.SeedID, open.wire.SeedTitle = protocol.Ptr(seed.ID), protocol.Ptr(seed.Title)
		if seed.StepSlug != "" {
			open.wire.SeedSlug = protocol.Ptr(seed.StepSlug)
		}
		open.profileID = seed.ProfileID
		return struct{}{}, nil
	})
	if err != nil {
		return openAsk{}, err
	}
	return open, nil
}

func (d *Daemon) projectAsks() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotAsks, func() {
		open := d.openAsks()
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := client.selectedProfile()
			data, ok := snapshots[profileID]
			if !ok {
				var err error
				data, err = json.Marshal(asksSnapshot(profileID, open))
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

func asksSnapshot(profileID string, open []openAsk) *protocol.AsksUpdatedMessage {
	message := &protocol.AsksUpdatedMessage{Event: protocol.EventAsksUpdated, ProfileID: profileID, Asks: []protocol.Ask{}}
	for _, a := range open {
		if askShownIn(profileID, a.profileID) {
			message.Asks = append(message.Asks, a.wire)
		}
	}
	return message
}

func askShownIn(profileID, askProfileID string) bool {
	return profileID != "" && askProfileID == profileID
}
