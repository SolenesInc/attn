package daemon

import (
	"context"
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
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	askRepliesForgetKind     = "ask_replies_forget"
	askRepliesForgetInterval = time.Hour
	askRepliesForgetTimeout  = time.Minute
	askReplyShownFor         = 24 * time.Hour
)

func (d *Daemon) asksSettingOn() bool {
	return parseBooleanSetting(d.homeSetting(SettingAsksEnabled))
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
		d.sendError(conn, fmt.Sprintf("%s closed before the ask was saved; ask again", to.SeedID()))
		return
	case err != nil:
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskOpenResult: &opened.wire})
	d.publishFact(FactAskOpened, id, nil)
}

func (d *Daemon) askAddressOf(session *protocol.Session, ref string) (inbox.Address, error) {
	seedID := ref
	if ref != "" {
		var err error
		if seedID, err = d.resolveSeedRef(ref, session.ProfileID); err != nil {
			return inbox.Address{}, err
		}
	}
	stake := d.openStake(session.ID)
	if seedID == "" || seedID == stake {
		if stake == "" {
			return d.inboxAddressOf(session.ID), nil
		}
		return inbox.ToSeed(stake), nil
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return inbox.Address{}, err
	}
	if seed.ProfileID != session.ProfileID {
		return inbox.Address{}, d.requireSeedInProfile(seed.ID, session.ProfileID, false)
	}
	to := inbox.ToSeed(seed.ID)
	if holder := d.inboxHolder(to); holder != nil && holder.ID == session.ID {
		return to, nil
	}
	if tender := seed.Tender(); tender.Named() {
		return inbox.Address{}, fmt.Errorf("%s is being tended by %s, and an ask is about a seed you tend; ask without --seed", seed.ID, tender.DisplayName())
	}
	return inbox.Address{}, fmt.Errorf("nobody is tending %s, and an ask is about a seed you tend; tend it first (attn seed tend %s), or ask without --seed", seed.ID, seed.ID)
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

func (d *Daemon) askAddressHeldBy(sessionID protocol.SessionID) func(inbox.Address) bool {
	roles := d.inboxRoleAddresses(sessionID)
	stake, _ := d.gardenDispatchCrown(sessionID)
	return func(to inbox.Address) bool {
		switch seedID := to.SeedID(); seedID {
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
	for _, a := range d.asks() {
		if a.stored.Reply == nil && held(a.stored.To) {
			result.Asks = append(result.Asks, a.wire)
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
	shown, err := d.readAsk(stored)
	if err != nil {
		return err
	}
	if !askShownIn(profileID, shown.profileID) {
		return errAskGone
	}
	text, err := ask.Reply(shown.wire, msg.Answers, protocol.Deref(msg.Note))
	if err != nil {
		return err
	}
	reply := protocol.AskReply{Answers: msg.Answers, Note: msg.Note}
	answer := inbox.Item{ID: uuid.NewString(), To: stored.To, Kind: inbox.AskAnswer, Source: stored.ID, Text: text}
	if seedID := stored.To.SeedID(); seedID != "" {
		err = d.noteAnswer(stored.ID, seedID, reply, answer)
	} else {
		err = d.store.AnswerAsk(stored.ID, reply, answer, time.Now())
	}
	if errors.Is(err, store.ErrAskNotOpen) {
		return errAskGone
	}
	if err != nil {
		return fmt.Errorf("save the answer: %w", err)
	}
	d.kickInboxAfterCommit(stored.To)
	d.publishFact(FactAskClosed, stored.ID, nil)
	if holder := d.inboxHolder(stored.To); holder != nil && holder.State != protocol.SessionStatePendingApproval {
		d.handBackTurn(holder.ID)
	}
	return nil
}

func (d *Daemon) noteAnswer(askID, seedID string, reply protocol.AskReply, answer inbox.Item) error {
	note := garden.Note{Seed: seedID, Kind: garden.NoteKindAnswer, Body: answer.Text}
	_, _, err := d.mintAndWriteNote(note, false, "", func(write store.DocumentWrite, fact store.BusEvent, events []store.BusEvent, now time.Time) (store.DocumentWriteResult, []int64, error) {
		return d.store.AnswerAskWithNote(askID, reply, answer, write, fact, events, now)
	})
	return err
}

func (d *Daemon) askRepliesForgetHandler(_ context.Context, _ *jobs.Job) (any, error) {
	ids, err := d.store.ForgetRepliesBefore(time.Now().Add(-askReplyShownFor))
	if err != nil {
		return nil, fmt.Errorf("forgetting ask replies: %w", err)
	}
	d.announceClosedAsks(ids)
	return map[string]any{"forgotten": len(ids)}, nil
}

func (d *Daemon) closeAsksTo(to inbox.Address) {
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
	parts, err := ask.Parse(a.Markdown)
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
		Reply:     a.Reply,
	}}
	switch {
	case a.To.SeedID() != "":
		seed, _, err := d.readSeed(a.To.SeedID())
		if err != nil {
			return shownAsk{}, err
		}
		shown.wire.SeedID, shown.wire.SeedTitle = protocol.Ptr(seed.ID), protocol.Ptr(seed.Title)
		if seed.StepSlug != "" {
			shown.wire.SeedSlug = protocol.Ptr(seed.StepSlug)
		}
		shown.profileID = seed.ProfileID
	case a.To.MemberID() != "":
		shown.wire.CrewMember = protocol.Ptr(a.To.MemberID())
		shown.profileID = d.crewProfileID(a.To.MemberID())
	case a.To.ChiefProfileID() != "":
		shown.wire.Chief = protocol.Ptr(true)
		shown.profileID = a.To.ChiefProfileID()
	default:
		if session := d.store.Get(a.SessionID); session != nil {
			shown.profileID = session.ProfileID
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
				data, err = json.Marshal(asksSnapshot(profileID, asks))
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

func asksSnapshot(profileID string, asks []shownAsk) *protocol.AsksUpdatedMessage {
	message := &protocol.AsksUpdatedMessage{Event: protocol.EventAsksUpdated, ProfileID: profileID, Asks: []protocol.Ask{}}
	for _, a := range asks {
		if askShownIn(profileID, a.profileID) {
			message.Asks = append(message.Asks, a.wire)
		}
	}
	return message
}

func askShownIn(profileID, askProfileID string) bool {
	return profileID != "" && askProfileID == profileID
}
