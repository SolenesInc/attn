package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statemarker"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	phonePushConsumer = "phone_push"

	PhonePushLinkWaitTimer stepclock.Name = "phone-push-link-wait"
)

// phonePushFreshFor drops a push for a wait or ask that began longer ago, so a daemon restart or a consumer that
// fell behind never floods the phone with old news. A push leaves 20 ms after the agent's reply, and StateSince
// survives a restart: a wait 128 s old sent nothing after one, while a wait 3 s old was pushed again 73 ms after
// it, under the same collapse id, so the phone replaces the first notification (s-h6j4pq).
const phonePushFreshFor = 2 * time.Minute

// APNs caps a notification at 4096 bytes. At these caps the sealed, base64url-encoded note stays near 2.6 KB.
const (
	phonePushTitleRunes = 100
	phonePushTextRunes  = 300
)

const (
	phonePushWaiting       = "waiting"
	phonePushNeedsApproval = "needs_approval"
	phonePushAsk           = "ask"
)

type phonePushNote struct {
	V           int    `json:"v"`
	HomeID      string `json:"home_id"`
	HomeName    string `json:"home_name"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	SessionID   string `json:"session_id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	AskID       string `json:"ask_id,omitempty"`

	collapseID string
	freshUntil time.Time
	episode    string
	agentTurn  string
}

type phonePushes struct {
	pushedEpisode   map[protocol.SessionID]string
	pushedAgentTurn map[protocol.SessionID]string
}

var phonePushFacts = bus.Filter{FactSessionStateChanged, FactAskOpened}

func (d *Daemon) registerPhonePush() {
	if d.eventBus == nil {
		return
	}
	pushes := &phonePushes{pushedEpisode: map[protocol.SessionID]string{}, pushedAgentTurn: map[protocol.SessionID]string{}}
	if err := d.eventBus.Register(phonePushConsumer, phonePushFacts, func(ctx context.Context, ev bus.Event) error {
		return d.pushToPhones(ctx, pushes, ev)
	}); err != nil {
		d.logf("phone push: %v", err)
	}
}

func (d *Daemon) unregisterPhonePush() {
	if d.eventBus == nil {
		return
	}
	if err := d.eventBus.Unregister(phonePushConsumer); err != nil {
		d.logf("phone push: %v", err)
	}
}

func (d *Daemon) pushToPhones(ctx context.Context, pushes *phonePushes, ev bus.Event) error {
	link := d.cloudLink()
	if link == nil {
		return nil
	}
	reached := map[string]bool{}
	waited := false
	for {
		nextConnect := link.NextConnect()
		note, ok := d.phoneNote(ctx, pushes, ev)
		if !ok {
			if waited {
				d.logf("phone push: dropped %s %s after waiting for the link: it no longer needs a push", ev.Name, ev.Subject)
			}
			return nil
		}
		linkDown := d.sendNote(link, note, reached)
		if linkDown == nil {
			if note.episode != "" {
				pushes.pushedEpisode[protocol.SessionID(note.SessionID)] = note.episode
			}
			if note.agentTurn != "" {
				pushes.pushedAgentTurn[protocol.SessionID(note.SessionID)] = note.agentTurn
			}
			return nil
		}
		patience := note.freshUntil.Sub(d.clock.Now())
		d.logf("phone push: waiting up to %s for the link to send %s %s: %v", patience, note.Kind, note.SessionID, linkDown)
		if err := d.awaitConnect(ctx, nextConnect, patience); err != nil {
			if errors.Is(err, errPhonePushExpired) {
				d.logf("phone push: dropped %s %s: the link stayed down past the %s freshness limit: %v", note.Kind, note.SessionID, phonePushFreshFor, linkDown)
				return nil
			}
			return err
		}
		waited = true
	}
}

var errPhonePushExpired = errors.New("the note outlived its freshness limit")

func (d *Daemon) awaitConnect(ctx context.Context, nextConnect <-chan struct{}, patience time.Duration) error {
	expired := d.clock.NewTimer(PhonePushLinkWaitTimer, patience)
	defer expired.Stop()
	select {
	case <-nextConnect:
		return nil
	case <-expired.C():
		return errPhonePushExpired
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Daemon) phoneNote(ctx context.Context, pushes *phonePushes, ev bus.Event) (phonePushNote, bool) {
	switch ev.Name {
	case FactSessionStateChanged:
		return d.waitingNote(ctx, pushes, protocol.SessionID(ev.Subject))
	case FactAskOpened:
		return d.askNote(ev.Subject)
	}
	return phonePushNote{}, false
}

func (d *Daemon) sendNote(link *cloudlink.Link, note phonePushNote, reached map[string]bool) (linkDown error) {
	pairings, err := phoneHome{d}.Pairings()
	if err != nil {
		d.logf("phone push: reading pairings: %v", err)
		return nil
	}
	note.V, note.HomeID, note.HomeName = 1, link.Status().HomeID, link.HomeName()
	if profile, err := d.store.GetProfile(note.ProfileID); err == nil {
		note.ProfileName = profile.Name
	}
	plaintext, err := json.Marshal(note)
	if err != nil {
		d.logf("phone push: encoding the note: %v", err)
		return nil
	}
	for _, pairing := range pairings {
		if pairing.ProfileID != note.ProfileID || reached[pairing.DeviceID] {
			continue
		}
		err := link.Push(pairing, plaintext, note.collapseID)
		switch {
		case errors.Is(err, cloudlink.ErrLinkDown):
			return err
		case err != nil:
			d.logf("phone push: %s %s for device %s: %v", note.Kind, note.SessionID, pairing.DeviceID, err)
		default:
			d.logf("phone push: sent %s %s to device %s", note.Kind, note.SessionID, pairing.DeviceID)
		}
		reached[pairing.DeviceID] = true
	}
	return nil
}

func (d *Daemon) waitingNote(ctx context.Context, pushes *phonePushes, sessionID protocol.SessionID) (phonePushNote, bool) {
	var session *protocol.Session
	if stored := d.store.Get(sessionID); stored != nil {
		session = d.sessionForBroadcast(stored)
	}
	if session == nil || !protocol.Deref(session.TurnOwed) {
		delete(pushes.pushedEpisode, sessionID)
		delete(pushes.pushedAgentTurn, sessionID)
		return phonePushNote{}, false
	}
	var kind, text string
	askedForUser := protocol.Deref(session.TurnByAgent)
	var agentTurn string
	if askedForUser {
		agentTurn = protocol.Deref(session.TurnOpenedAt)
	}
	notificationStop, sameState := d.stopFollowsNotification(session)
	if !sameState {
		return phonePushNote{}, false
	}
	announced := agentTurn != "" && pushes.pushedAgentTurn[sessionID] == agentTurn
	switch {
	case session.State == protocol.SessionStateWaitingInput && !notificationStop:
		kind = phonePushWaiting
	case (session.State == protocol.SessionStateWaitingInput || session.State == protocol.SessionStateIdle) && askedForUser && !announced:
		kind = phonePushWaiting
	case session.State == protocol.SessionStatePendingApproval:
		kind = phonePushNeedsApproval
	default:
		return phonePushNote{}, false
	}
	episode := string(session.State) + "@" + session.StateSince
	since := protocol.Timestamp(session.StateSince).Time()
	if d.clock.Now().Sub(since) > phonePushFreshFor || pushes.pushedEpisode[sessionID] == episode {
		return phonePushNote{}, false
	}
	if kind == phonePushWaiting {
		text = lastParagraphTail(d.lastAssistantMessage(ctx, sessionID), phonePushTextRunes)
	}
	sum := sha256.Sum256([]byte(string(sessionID) + "\n" + episode))
	return phonePushNote{
		ProfileID:  session.ProfileID,
		SessionID:  string(sessionID),
		Title:      clipRunes(d.phoneSessionTitle(session), phonePushTitleRunes),
		Kind:       kind,
		Text:       text,
		collapseID: hex.EncodeToString(sum[:16]),
		freshUntil: since.Add(phonePushFreshFor),
		episode:    episode,
		agentTurn:  agentTurn,
	}, true
}

func (d *Daemon) askNote(askID string) (phonePushNote, bool) {
	stored, err := d.store.GetAsk(askID)
	if err != nil {
		d.logf("phone push: reading ask %s: %v", askID, err)
		return phonePushNote{}, false
	}
	shown, err := d.readAsk(stored)
	if err != nil || shown.profileID == "" {
		return phonePushNote{}, false
	}
	created, err := time.Parse(time.RFC3339Nano, stored.CreatedAt)
	if err != nil || d.clock.Now().Sub(created) > phonePushFreshFor || stored.Reply != nil {
		return phonePushNote{}, false
	}
	title := protocol.Deref(shown.wire.SeedTitle)
	if session := d.store.Get(stored.SessionID); session != nil {
		title = d.phoneSessionTitle(session)
	}
	return phonePushNote{
		ProfileID:  shown.profileID,
		SessionID:  string(stored.SessionID),
		Title:      clipRunes(title, phonePushTitleRunes),
		Kind:       phonePushAsk,
		Text:       clipRunes(stored.Subject, phonePushTextRunes),
		AskID:      askID,
		collapseID: "ask-" + askID,
		freshUntil: created.Add(phonePushFreshFor),
	}, true
}

func (d *Daemon) phoneSessionTitle(session *protocol.Session) string {
	d.decorateSessionWithTitle(session)
	if title := protocol.Deref(session.Title); title != "" {
		return title
	}
	return session.Label
}

func (d *Daemon) lastAssistantMessage(ctx context.Context, sessionID protocol.SessionID) string {
	answer, err := Call[protocol.MonitorLastAssistantResult](ctx, d.router(), protocol.MonitorLastAssistantMessage{Cmd: protocol.CmdMonitorLastAssistant, SessionID: sessionID})
	if err != nil {
		return ""
	}
	return statemarker.Strip(answer.Text)
}

func lastParagraphTail(text string, limit int) string {
	text = strings.TrimSpace(text)
	if i := strings.LastIndex(text, "\n\n"); i >= 0 {
		text = strings.TrimSpace(text[i:])
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return "…" + strings.TrimSpace(string(runes[len(runes)-limit+1:]))
}

func clipRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
