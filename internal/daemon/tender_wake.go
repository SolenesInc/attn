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
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	tenderWakeLimit             = 48
	tenderWakeWindow            = 24 * time.Hour
	notificationKindWakeBlocked = "tender_wake_blocked"
	notificationActionOpenSeed  = "open_seed"
)

type tenderWakeSetting string

const (
	tenderWakeAny     tenderWakeSetting = "any"
	tenderWakeWatches tenderWakeSetting = "watches"
	tenderWakeOff     tenderWakeSetting = "off"
)

func parseTenderWakeSetting(value string) (tenderWakeSetting, error) {
	switch setting := tenderWakeSetting(strings.ToLower(strings.TrimSpace(value))); setting {
	case "":
		return tenderWakeAny, nil
	case tenderWakeAny, tenderWakeWatches, tenderWakeOff:
		return setting, nil
	}
	return "", fmt.Errorf("garden.tender_wake is any, watches or off, not %q", value)
}

func (d *Daemon) tenderWakeSetting(profileID string) tenderWakeSetting {
	setting, err := parseTenderWakeSetting(d.store.ProfileSetting(profileID, string(SettingGardenTenderWake)))
	if err != nil {
		return tenderWakeAny
	}
	return setting
}

type tenderWakeLedger struct {
	mu      sync.Mutex
	stamps  map[string][]time.Time
	blocked map[string]string
}

func (l *tenderWakeLedger) reopensAt(seedID string, now time.Time) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.pruneLocked(seedID, now)
	if len(kept) < tenderWakeLimit {
		return time.Time{}, false
	}
	return kept[0].Add(tenderWakeWindow), true
}

func (l *tenderWakeLedger) charge(seedID string, now time.Time) int {
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

func (l *tenderWakeLedger) pruneLocked(seedID string, now time.Time) []time.Time {
	cutoff := now.Add(-tenderWakeWindow)
	kept := slices.DeleteFunc(l.stamps[seedID], func(at time.Time) bool { return !at.After(cutoff) })
	if len(kept) == 0 {
		delete(l.stamps, seedID)
		return nil
	}
	l.stamps[seedID] = kept
	return kept
}

func (l *tenderWakeLedger) forget(seedID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.stamps, seedID)
	delete(l.blocked, seedID)
}

func (l *tenderWakeLedger) block(seedID, reason string) bool {
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

func (l *tenderWakeLedger) unblock(seedID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, blocked := l.blocked[seedID]
	delete(l.blocked, seedID)
	return blocked
}

func (l *tenderWakeLedger) blockedReason(seedID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blocked[seedID]
}

type tenderWakeDelivery struct {
	session protocol.SessionID
	seedID  string
	title   string
	note    string
	claude  bool
}

func newTenderWake(session protocol.SessionID, seed garden.Seed, standby *garden.Standby, claude bool) *tenderWakeDelivery {
	return &tenderWakeDelivery{session: session, seedID: seed.ID, title: seed.Title, note: standby.Note, claude: claude}
}

func (d *Daemon) memberTenderWake(seedID string, session protocol.SessionID) (*tenderWakeDelivery, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	standby, err := d.seedStandby(seed)
	if err != nil || standby == nil {
		return nil, err
	}
	return newTenderWake(session, seed, standby, false), nil
}

type tenderWakeTripwire struct{ reopensAt time.Time }

func (e tenderWakeTripwire) Error() string {
	return fmt.Sprintf("tender_wake_tripwire=%d per %s; the seed's tender was woken %d times in the last %s, which no healthy seed does, so attn stopped waking it until %s",
		tenderWakeLimit, tenderWakeWindow, tenderWakeLimit, tenderWakeWindow, e.reopensAt.UTC().Format(time.RFC3339))
}

func (d *Daemon) seedMailWakes(a who.Address, profileID string) (bool, tenderWakeSetting, error) {
	setting := d.tenderWakeSetting(profileID)
	switch setting {
	case tenderWakeOff:
		return false, setting, nil
	case tenderWakeAny:
		return true, setting, nil
	}
	deliveries, err := d.store.UnreadInboxDeliveries(a)
	if err != nil {
		return false, setting, err
	}
	for _, delivery := range deliveries {
		if delivery.Item.Attempts < inbox.MaxAttempts && watchOrReminder(delivery.Item.Item) {
			return true, setting, nil
		}
	}
	return false, setting, nil
}

func watchOrReminder(item inbox.Item) bool {
	return item.Kind == inbox.Notice && (item.Source == seedReminderSource || item.Hint == pullRequestWatchHint)
}

func (d *Daemon) wakeStandbyTenderLocked(a who.Address, seedID string, state *inboxDeliveryState, now time.Time) (inbox.Receipt, error) {
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
	wakes, setting, err := d.seedMailWakes(a, seed.ProfileID)
	if err != nil {
		return receipt, err
	}
	if !wakes {
		receipt.Detail = fmt.Sprintf("queued: %s is on standby, and garden.tender_wake=%s does not wake its tender for this mail", seed.ID, setting)
		return receipt, nil
	}
	claude := false
	if entry := d.store.SessionLedgerEntry(sessionID); entry != nil && entry.Agent == string(protocol.SessionAgentClaude) {
		claude = true
		if !d.claudeModEnabled() {
			receipt.Detail = d.blockTenderWake(a, seed, fmt.Sprintf("its tender is a Claude session, and %s is off, so attn cannot deliver to it once resumed; turn %s on", SettingClaudeModEnabled, SettingClaudeModEnabled))
			return receipt, nil
		}
	}
	if reopensAt, tripped := d.tenderWakes.reopensAt(seed.ID, d.clock.Now()); tripped {
		receipt.Detail = d.blockTenderWake(a, seed, tenderWakeTripwire{reopensAt: reopensAt}.Error())
		d.armInboxLocked(a, state, reopensAt.Sub(d.clock.Now()))
		return receipt, nil
	}
	started, err := d.store.StampInboxAttempt(a, now)
	if err != nil || !started {
		return receipt, err
	}
	d.logInboxExhaustion(a)
	count := d.tenderWakes.charge(seed.ID, d.clock.Now())
	var outcome *seedResumeOutcome
	err = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var wakeErr error
		outcome, wakeErr = d.wakeSeedTender(protection, seed, tenderWakeCause{
			mail:   true,
			tend:   func(who.Party, who.Actor) error { return nil },
			settle: func() {},
		})
		return wakeErr
	})
	if blocked := (tenderWakeBlocked{}); errors.As(err, &blocked) {
		receipt.Detail = d.blockTenderWake(a, seed, blocked.Error())
		return receipt, nil
	}
	if err != nil {
		d.logf("tender wake: seed %s: reopening session %s failed, retrying in %s: %v", seed.ID, sessionID, inbox.AttemptDelay, err)
		d.closeTenderAfterFailedWake(sessionID, err)
		if remaining, attemptErr := d.store.InboxAttempt(a); attemptErr == nil && remaining.Live == 0 {
			receipt.Detail = d.blockTenderWake(a, seed, err.Error())
			return receipt, nil
		}
		receipt.Detail = err.Error()
		d.armInboxLocked(a, state, inbox.AttemptDelay)
		return receipt, nil
	}
	d.unblockTenderWake(seed.ID)
	d.logf("tender wake: seed %s reopened session %s for %s (wake %d of %d in %s)", seed.ID, outcome.SessionID, d.seedMailCause(a), count, tenderWakeLimit, tenderWakeWindow)
	if outcome.AlreadyRunning {
		d.kickInbox(a)
		receipt.Detail = "notified the running tender of " + seed.ID
		return receipt, nil
	}
	state.wakeSession = outcome.SessionID
	state.tenderWake = newTenderWake(outcome.SessionID, seed, standby, claude)
	d.kickInbox(a)
	receipt.Detail = fmt.Sprintf("reopened session %s, the tender of %s on standby; the mail is delivered once it reads input", shortSessionID(outcome.SessionID), seed.ID)
	return receipt, nil
}

func (d *Daemon) closeTenderAfterFailedWake(sessionID protocol.SessionID, failure error) {
	if d.store.Get(sessionID) == nil || d.sessionHasLiveWorker(sessionID) {
		return
	}
	closing, err := d.beginSessionClose(sessionID, store.SessionClose{By: who.Attn(), Reason: "attn could not resume it for seed mail: " + failure.Error()}, nil)
	if err != nil {
		d.logf("tender wake: closing session %s after its failed wake: %v", sessionID, err)
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

func (d *Daemon) blockTenderWake(a who.Address, seed garden.Seed, reason string) string {
	detail := fmt.Sprintf("queued: %s is on standby, but attn cannot wake its tender: %s", seed.ID, reason)
	if !d.tenderWakes.block(seed.ID, reason) {
		return detail
	}
	d.publishStandbyViewChanged(who.ToTenderOf(seed.ID))
	item := d.seedMailCause(a)
	d.logf("tender wake: seed %s blocked for %s: %s", seed.ID, item, reason)
	body := fmt.Sprintf("attn could not wake this seed's tender for %s: %s. The mail waits in the seed's mailbox, and attn does not retry this wake by itself.", item, reason)
	_, memberTender := seed.Tender.Member()
	sessionTender, _ := seed.Tender.Session()
	if _, err := d.appendSeedNote(seed.ID, body, who.Attn(), garden.NoteKindNote, nil, !memberTender, sessionTender); err != nil {
		d.logf("tender wake: note the block on %s: %v", seed.ID, err)
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindWakeBlocked,
		Severity:   store.NotificationCritical,
		Title:      fmt.Sprintf("%s could not wake its tender", seed.ID),
		Body:       fmt.Sprintf("%s %q is on standby and got %s, but attn could not wake its tender: %s", seed.ID, seed.Title, item, reason),
		Impact:     "The mail waits in the seed's mailbox until someone tends the seed.",
		Actions:    []store.NotificationAction{{Kind: notificationActionOpenSeed, Label: "Open seed", TargetID: protocol.SessionID(seed.ID)}},
		SourceKind: "seed",
		SourceID:   seed.ID,
		ProfileID:  seed.ProfileID,
	}, time.Now())
	if err != nil {
		d.logf("tender wake: notify about %s: %v", seed.ID, err)
		return detail
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
	return detail
}

func (d *Daemon) unblockTenderWake(seedID string) {
	if d.tenderWakes.unblock(seedID) {
		d.publishStandbyViewChanged(who.ToTenderOf(seedID))
	}
}

func (d *Daemon) tenderWakeText(wake *tenderWakeDelivery) string {
	return prompts.RenderText("session", "tender-wake", prompts.Values{
		"seed_id":    wake.seedID,
		"seed_title": wake.title,
		"note":       wake.note,
	})
}

func (d *Daemon) tenderWakeModMissed(session protocol.SessionID) {
	addresses, err := d.inboxAddressesOf(session)
	if err != nil {
		d.logf("tender wake: addresses of %s: %v", session, err)
		return
	}
	for _, a := range addresses {
		if _, seedMail := a.TenderOf(); seedMail {
			d.blockClaudeWakeWithoutMod(a, session)
		}
	}
}

func (d *Daemon) blockClaudeWakeWithoutMod(a who.Address, session protocol.SessionID) {
	state := d.lockInboxState(a)
	defer state.unlock()
	wake := state.tenderWake
	if wake == nil || !wake.claude || wake.session != session || state.wakeSession != session {
		return
	}
	seed, _, err := d.readSeed(wake.seedID)
	if err != nil {
		d.logf("tender wake: %s: %v", wake.seedID, err)
		return
	}
	d.blockTenderWake(a, seed, fmt.Sprintf("attn resumed Claude session %s, but its Claude Code mod never polled within %s, so attn did not type the mail into it; the mail is delivered if the mod polls later", shortSessionID(session), claudeModFirstPollTripwire))
}
