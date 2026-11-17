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
	images, err := parseAskImages(msg.Images)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	to, err := d.askAddressOf(session, strings.TrimSpace(protocol.Deref(msg.SeedID)))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if key, _ := to.Member(); !key.IsZero() {
		if _, retired := d.retiredCrewMember(key.String()); retired {
			d.sendError(conn, "you are retired, so the user takes no new asks from you; file your letter with `attn handoff`")
			return
		}
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
		BaseDir:   msg.BaseDir,
		CreatedAt: d.clock.Now().UTC().Format(time.RFC3339Nano),
	}
	opened, err := d.describeAsk(stored, parts)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := writeAskImages(id, images); err != nil {
		d.sendError(conn, fmt.Sprintf("keep the ask's images: %v", err))
		return
	}
	err = d.store.AddAsk(stored)
	if err != nil {
		d.forgetAskImages([]string{id})
	}
	switch {
	case errors.Is(err, store.ErrAskerGone):
		d.sendError(conn, errSessionNotLive(session.ID).Error())
		return
	case errors.Is(err, store.ErrAskSeedClosed):
		seedID, _ := to.TenderOf()
		d.sendError(conn, fmt.Sprintf("%s closed before the ask was saved; ask again", seedID))
		return
	case err != nil:
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskOpenResult: &opened.wire})
	d.publishFact(FactAskOpened, id, nil)
}

func (d *Daemon) askAddressOf(session *protocol.Session, ref string) (who.Address, error) {
	seedID := ref
	if ref != "" {
		var err error
		if seedID, err = d.resolveSeedRef(ref, session.ProfileID); err != nil {
			return who.Address{}, err
		}
	}
	stake := d.openStake(session.ID)
	if seedID == "" || seedID == stake {
		if stake == "" {
			return d.inboxAddressOf(session.ID), nil
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
	if !seed.Tender.IsZero() {
		return who.Address{}, fmt.Errorf("%s is being tended by %s, and an ask is about a seed you tend; ask without --seed", seed.ID, d.partyName(seed.Tender))
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
	roles := d.inboxRoleAddresses(sessionID)
	stake, _ := d.gardenDispatchCrown(sessionID)
	return func(to who.Address) bool {
		seedID, seed := to.TenderOf()
		switch {
		case !seed:
			return slices.Contains(roles, to)
		case seedID == stake:
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
		return nil, errors.New("asks are off for this session because it launched without them; ask in the conversation instead, and run `attn turn open` if a notification started your run")
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

func (d *Daemon) handleAskList(conn net.Conn, msg *protocol.AskListMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		d.sendError(conn, errSessionNotLive(sessionID).Error())
		return
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
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskListResult: result})
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

func (d *Daemon) handleAskShow(conn net.Conn, msg *protocol.AskShowMessage) {
	shown, reach, err := d.reachableOpenAsk(protocol.TrimID(msg.SessionID), msg.AskID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AskShowResult: &protocol.AskShowResult{
		Ask: shown.wire, Markdown: shown.stored.Markdown, From: d.actorView(d.askerOf(shown.stored).Actor()), Held: reach == askHeld,
	}})
}

func (d *Daemon) handleAskClose(conn net.Conn, msg *protocol.AskCloseMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
	shown, reach, err := d.reachableOpenAsk(sessionID, msg.AskID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	reason := strings.TrimSpace(protocol.Deref(msg.Reason))
	if reach == askOverseen && reason == "" {
		d.sendError(conn, fmt.Sprintf("%s is not your ask. Say why you withdraw it, and attn tells its asker: attn ask close %s -m \"<why>\"",
			shown.stored.ID, shown.stored.ID))
		return
	}
	if err := d.store.CloseAsk(shown.stored.ID); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if reach == askOverseen {
		d.tellWithdrawnAsk(sessionID, shown.stored, reason)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
	d.announceClosedAsks([]string{shown.stored.ID})
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
	text, err := ask.Reply(shown.wire, msg.Answers, protocol.Deref(msg.Note))
	if err != nil {
		return err
	}
	reply := protocol.AskReply{Answers: msg.Answers, Note: msg.Note}
	answer := inbox.Item{ID: uuid.NewString(), To: stored.To, Kind: inbox.AskAnswer, Source: stored.ID, Text: text}
	if seedID, seed := stored.To.TenderOf(); seed {
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

type openAsksError struct {
	seedID  string
	asks    []store.Ask
	standby bool
}

func (e *openAsksError) Refusal() protocol.Refusal { return protocol.RefusalWrongState }

func (e *openAsksError) Error() string {
	asks := make([]string, len(e.asks))
	for i, ask := range e.asks {
		asks[i] = fmt.Sprintf("%s (%q)", ask.ID, ask.Subject)
	}
	open := fmt.Sprintf("%s has %d open ask(s) to the user: %s.", e.seedID, len(asks), strings.Join(asks, ", "))
	if e.standby {
		return fmt.Sprintf("%s A session that waits on the user stays open, so the user can find it: end your turn, and attn types the answer into this conversation. If the user told you to close, run `attn seed standby %s --stay -m \"…\"`, then `attn agent close --self -m \"…\"`",
			open, e.seedID)
	}
	return fmt.Sprintf("%s The answer comes to whoever tends the seed: end your turn, and harvest after you act on the answer. Withdraw an ask that no longer matters with `attn ask close <id>`. If you do not tend the seed, `attn seed tend %s` first",
		open, e.seedID)
}

func (d *Daemon) refuseHarvestWithOpenAsks(seedID string) error {
	return d.refuseWithOpenAsks(seedID, false)
}

func (d *Daemon) refuseClosingStandbyWithOpenAsks(seedID string) error {
	return d.refuseWithOpenAsks(seedID, true)
}

func (d *Daemon) refuseWithOpenAsks(seedID string, standby bool) error {
	asks, err := d.store.OpenAsksTo(who.ToTenderOf(seedID))
	if err != nil {
		return fmt.Errorf("reading the open asks about %s: %w", seedID, err)
	}
	if len(asks) > 0 {
		return &openAsksError{seedID: seedID, asks: asks, standby: standby}
	}
	return nil
}

func (d *Daemon) closeSeedAsks(seedID string) []protocol.SeedClosedAsk {
	to := who.ToTenderOf(seedID)
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
	}
	return closed
}

func (d *Daemon) announceClosedAsks(ids []string) {
	if len(ids) == 0 {
		return
	}
	d.forgetAskImages(ids)
	d.coalesceSnapshots(func() {
		for _, id := range ids {
			d.publishFact(FactAskClosed, id, nil)
		}
	})
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
