package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	tenderResumeLimit             = 48
	tenderResumeWindow            = 24 * time.Hour
	notificationKindResumeBlocked = "tender_wake_blocked"
	notificationActionOpenSeed    = "open_seed"
)

var errTenderOutpostLost = errors.New("the tender's outpost did not answer the resume's checks")

type tenderResumeSetting string

const (
	tenderResumeAny     tenderResumeSetting = "any"
	tenderResumeWatches tenderResumeSetting = "watches"
	tenderResumeOff     tenderResumeSetting = "off"
)

func parseTenderResumeSetting(value string) (tenderResumeSetting, error) {
	switch setting := tenderResumeSetting(strings.ToLower(strings.TrimSpace(value))); setting {
	case "":
		return tenderResumeAny, nil
	case tenderResumeAny, tenderResumeWatches, tenderResumeOff:
		return setting, nil
	}
	return "", fmt.Errorf("garden.tender_resume is any, watches or off, not %q", value)
}

func (d *Daemon) tenderResumeSetting(profileID string) tenderResumeSetting {
	setting, err := parseTenderResumeSetting(d.store.ProfileSetting(profileID, setting.GardenTenderResume.Key))
	if err != nil {
		return tenderResumeAny
	}
	return setting
}

type tenderResumeLedger struct {
	mu      sync.Mutex
	stamps  map[string][]time.Time
	blocked map[string]string
}

func (l *tenderResumeLedger) reopensAt(seedID string, now time.Time) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.pruneLocked(seedID, now)
	if len(kept) < tenderResumeLimit {
		return time.Time{}, false
	}
	return kept[0].Add(tenderResumeWindow), true
}

func (l *tenderResumeLedger) charge(seedID string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stamps == nil {
		l.stamps = make(map[string][]time.Time)
	}
	for id := range l.stamps {
		l.pruneLocked(id, now)
	}
	l.stamps[seedID] = append(l.stamps[seedID], now)
	return len(l.stamps[seedID])
}

func (l *tenderResumeLedger) pruneLocked(seedID string, now time.Time) []time.Time {
	cutoff := now.Add(-tenderResumeWindow)
	kept := slices.DeleteFunc(l.stamps[seedID], func(at time.Time) bool { return !at.After(cutoff) })
	if len(kept) == 0 {
		delete(l.stamps, seedID)
		return nil
	}
	l.stamps[seedID] = kept
	return kept
}

func (l *tenderResumeLedger) forget(seedID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.stamps, seedID)
	delete(l.blocked, seedID)
}

func (l *tenderResumeLedger) block(seedID, reason string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blocked[seedID] == reason {
		return false
	}
	if l.blocked == nil {
		l.blocked = make(map[string]string)
	}
	l.blocked[seedID] = reason
	return true
}

func (l *tenderResumeLedger) unblock(seedID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, blocked := l.blocked[seedID]
	delete(l.blocked, seedID)
	return blocked
}

func (l *tenderResumeLedger) blockedReason(seedID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blocked[seedID]
}

type tenderResumeDelivery struct {
	session protocol.SessionID
	seedID  string
	title   string
	note    string
	claude  bool
}

func newTenderResume(session protocol.SessionID, seed garden.Seed, standby *garden.Standby, claude bool) *tenderResumeDelivery {
	return &tenderResumeDelivery{session: session, seedID: seed.ID, title: seed.Title, note: standby.Note, claude: claude}
}

func (d *Daemon) memberTenderResume(seedID string, session protocol.SessionID) (*tenderResumeDelivery, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	standby, err := d.seedStandby(seed)
	if err != nil || standby == nil {
		return nil, err
	}
	return newTenderResume(session, seed, standby, false), nil
}

type tenderResumeTripwire struct{ reopensAt time.Time }

func (e tenderResumeTripwire) Error() string {
	return fmt.Sprintf("tender_resume_tripwire=%d per %s; attn resumed the seed's tender %d times in the last %s, which no healthy seed does, so attn stopped resuming it until %s",
		tenderResumeLimit, tenderResumeWindow, tenderResumeLimit, tenderResumeWindow, e.reopensAt.UTC().Format(time.RFC3339))
}

func (d *Daemon) seedMailResumes(a who.Address, profileID string) (bool, tenderResumeSetting, error) {
	chosen := d.tenderResumeSetting(profileID)
	switch chosen {
	case tenderResumeOff:
		return false, chosen, nil
	case tenderResumeAny:
		return true, chosen, nil
	}
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil {
		return false, chosen, err
	}
	for _, delivery := range deliveries {
		if delivery.Item.Attempts < inbox.MaxAttempts && watchOrReminder(delivery.Item.Item) {
			return true, chosen, nil
		}
	}
	return false, chosen, nil
}

func watchOrReminder(item inbox.Item) bool {
	return item.Kind == inbox.Notice && (item.Source == seedReminderSource || item.Hint == pullRequestWatchHint)
}

func (d *Daemon) resumeStandbyTenderLocked(a who.Address, seedID string, state *inboxDeliveryState, now time.Time) (inbox.Receipt, error) {
	receipt := inbox.Receipt{Detail: "queued (seed has no reachable tender; waits for its next tender)"}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return receipt, err
	}
	standby, err := d.seedStandby(seed)
	if err != nil {
		return receipt, err
	}
	sessionID, session := seed.Tender.Session()
	if !session || standby == nil {
		return receipt, nil
	}
	resumes, chosen, err := d.seedMailResumes(a, seed.ProfileID)
	if err != nil {
		return receipt, err
	}
	if !resumes {
		receipt.Detail = fmt.Sprintf("queued: %s is on standby, and garden.tender_resume=%s does not resume its tender for this mail", seed.ID, chosen)
		return receipt, nil
	}
	waitForOutpost := func() (inbox.Receipt, error) {
		receipt.Detail = fmt.Sprintf("queued: %s is on standby, and its tender's outpost %s is unreachable; attn tries again when it is back, or in %s", seed.ID, d.outpostName(d.sessionHost(sessionID)), inbox.AttemptDelay)
		d.armInboxLocked(a, state, inbox.AttemptDelay)
		return receipt, nil
	}
	if d.unreachableSessionHost(sessionID) != "" {
		return waitForOutpost()
	}
	claude := false
	if entry := d.store.SessionLedgerEntry(sessionID); entry != nil && entry.Agent == string(protocol.SessionAgentClaude) {
		claude = true
	}
	if reopensAt, tripped := d.tenderResumes.reopensAt(seed.ID, d.clock.Now()); tripped {
		receipt.Detail = d.blockTenderResume(a, seed, tenderResumeTripwire{reopensAt: reopensAt}.Error())
		d.armInboxLocked(a, state, reopensAt.Sub(d.clock.Now()))
		return receipt, nil
	}
	var outcome *seedResumeOutcome
	var count int
	err = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		continuation := d.continuationOnItsMachine(seed)
		if continuation != nil && continuation.OutpostLost {
			d.logf("tender resume: seed %s waits for its tender's outpost: %s", seed.ID, continuation.ResumeReason)
			return errTenderOutpostLost
		}
		started, stampErr := d.store.StampInboxAttempt(a, now)
		if stampErr != nil {
			return stampErr
		}
		if !started {
			return errInboxNoUnread
		}
		d.logInboxExhaustion(a)
		count = d.tenderResumes.charge(seed.ID, d.clock.Now())
		var resumeErr error
		outcome, resumeErr = d.resumeFromContinuation(protection, seed, continuation, tenderResumeCause{
			mail:   true,
			by:     who.Attn(),
			tend:   func(who.Party, who.Actor) error { return nil },
			settle: func() {},
		})
		return resumeErr
	})
	if errors.Is(err, errTenderOutpostLost) {
		return waitForOutpost()
	}
	if errors.Is(err, errInboxNoUnread) {
		return receipt, nil
	}
	if blocked := (tenderResumeBlocked{}); errors.As(err, &blocked) {
		receipt.Detail = d.blockTenderResume(a, seed, blocked.Error())
		return receipt, nil
	}
	if err != nil {
		d.logf("tender resume: seed %s: reopening session %s failed, retrying in %s: %v", seed.ID, sessionID, inbox.AttemptDelay, err)
		d.closeTenderAfterFailedResume(sessionID, err)
		if remaining, attemptErr := d.store.InboxAttempt(a); attemptErr == nil && remaining.Live == 0 {
			receipt.Detail = d.blockTenderResume(a, seed, err.Error())
			return receipt, nil
		}
		receipt.Detail = err.Error()
		d.armInboxLocked(a, state, inbox.AttemptDelay)
		return receipt, nil
	}
	d.unblockTenderResume(seed.ID)
	d.logf("tender resume: seed %s reopened session %s for %s (resume %d of %d in %s)", seed.ID, outcome.SessionID, d.seedMailCause(a), count, tenderResumeLimit, tenderResumeWindow)
	if outcome.AlreadyRunning {
		d.kickInbox(a)
		receipt.Detail = "notified the running tender of " + seed.ID
		return receipt, nil
	}
	state.startedSession = outcome.SessionID
	state.tenderResume = newTenderResume(outcome.SessionID, seed, standby, claude)
	d.kickInbox(a)
	receipt.Detail = fmt.Sprintf("reopened session %s, the tender of %s on standby; the mail is delivered once it reads input", shortSessionID(outcome.SessionID), seed.ID)
	return receipt, nil
}

func (d *Daemon) kickStandbyTendersOn(host string) {
	addresses, err := d.store.UnreadInboxAddresses()
	if err != nil {
		d.logf("inbox: outpost %s back: %v", host, err)
		return
	}
	for _, address := range addresses {
		seedID, seedMail := address.TenderOf()
		if !seedMail || d.tenderResumes.blockedReason(seedID) != "" {
			continue
		}
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			d.logf("inbox: outpost %s back: %v", host, err)
			continue
		}
		if tender, session := seed.Tender.Session(); session && d.sessionHost(tender) == host {
			d.kickInbox(address)
		}
	}
}

func (d *Daemon) closeTenderAfterFailedResume(sessionID protocol.SessionID, failure error) {
	if d.store.Get(sessionID) == nil || d.sessionHasLiveWorker(sessionID) {
		return
	}
	closing, err := d.beginSessionClose(sessionID, store.SessionClose{By: who.Attn(), Reason: "attn could not resume it for seed mail: " + failure.Error()}, nil)
	if err != nil {
		d.logf("tender resume: closing session %s after its failed resume: %v", sessionID, err)
		return
	}
	d.finishSessionClose(sessionID, closing)
}

func (d *Daemon) seedMailCause(a who.Address) string {
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil || len(deliveries) == 0 {
		return "seed mail"
	}
	return describeInboxItem(deliveries[0].Item)
}

func describeInboxItem(item store.InboxItem) string {
	what := strings.TrimSpace(item.Hint)
	switch {
	case item.Source == seedReminderSource:
		what = "a reminder"
	case item.Kind == inbox.PeerMessage:
		what = "a message"
	case what == "":
		what = string(item.Kind)
	}
	return fmt.Sprintf("%s (inbox item %s)", what, item.ID)
}

func (d *Daemon) blockTenderResume(a who.Address, seed garden.Seed, reason string) string {
	detail := fmt.Sprintf("queued: %s is on standby, but attn cannot resume its tender: %s", seed.ID, reason)
	if !d.tenderResumes.block(seed.ID, reason) {
		return detail
	}
	d.publishStandbyViewChanged(who.ToTenderOf(seed.ID))
	item := d.seedMailCause(a)
	d.logf("tender resume: seed %s blocked for %s: %s", seed.ID, item, reason)
	body := fmt.Sprintf("attn could not resume this seed's tender for %s: %s. The mail waits in the seed's mailbox, and attn does not retry this resume by itself.", item, reason)
	_, memberTender := seed.Tender.Member()
	sessionTender, _ := seed.Tender.Session()
	if _, err := d.appendSeedNote(seed.ID, body, who.Attn(), garden.NoteKindNote, nil, !memberTender, sessionTender); err != nil {
		d.logf("tender resume: note the block on %s: %v", seed.ID, err)
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindResumeBlocked,
		Severity:   store.NotificationCritical,
		Title:      fmt.Sprintf("%s could not resume its tender", seed.ID),
		Body:       fmt.Sprintf("%s %q is on standby and got %s, but attn could not resume its tender: %s", seed.ID, seed.Title, item, reason),
		Impact:     "The mail waits in the seed's mailbox until someone tends the seed.",
		Actions:    []store.NotificationAction{{Kind: notificationActionOpenSeed, Label: "Open seed", TargetID: protocol.SessionID(seed.ID)}},
		SourceKind: "seed",
		SourceID:   seed.ID,
		ProfileID:  seed.ProfileID,
	}, time.Now())
	if err != nil {
		d.logf("tender resume: notify about %s: %v", seed.ID, err)
		return detail
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
	return detail
}

func (d *Daemon) unblockTenderResume(seedID string) {
	if d.tenderResumes.unblock(seedID) {
		d.publishStandbyViewChanged(who.ToTenderOf(seedID))
	}
}

func (d *Daemon) tenderResumeText(delivery *tenderResumeDelivery) string {
	return prompts.RenderText("session", "tender-resume", prompts.Values{
		"seed_id":    delivery.seedID,
		"seed_title": delivery.title,
		"note":       delivery.note,
	})
}

func (d *Daemon) tenderResumeModMissed(session protocol.SessionID, claudeVersion string) {
	addresses, err := d.inboxAddressesOf(session)
	if err != nil {
		d.logf("tender resume: addresses of %s: %v", session, err)
		return
	}
	for _, a := range addresses {
		if _, seedMail := a.TenderOf(); seedMail {
			d.blockClaudeResumeWithoutMod(a, session, claudeVersion)
		}
	}
}

func (d *Daemon) blockClaudeResumeWithoutMod(a who.Address, session protocol.SessionID, claudeVersion string) {
	state := d.lockInboxState(a)
	defer state.unlock()
	delivery := state.tenderResume
	if delivery == nil || !delivery.claude || delivery.session != session || state.startedSession != session {
		return
	}
	seed, _, err := d.readSeed(delivery.seedID)
	if err != nil {
		d.logf("tender resume: %s: %v", delivery.seedID, err)
		return
	}
	d.blockTenderResume(a, seed, fmt.Sprintf("attn resumed Claude session %s, but the mod in %s never polled within %s, so attn did not type the mail into it; the mail is delivered if the mod polls later. If this Claude Code version broke the mod, attn needs a fix for that version", shortSessionID(session), claudeVersion, claudeModFirstPollTripwire))
}
