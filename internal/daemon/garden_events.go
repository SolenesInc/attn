package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/store"
)

const gardenSeedBellConsumer = "garden-seed-bells"

var gardenSeedEventModel, gardenSeedEventVocabulary = mustBuildGardenSeedEvents()

func mustBuildGardenSeedEvents() (*events.Model, events.Vocabulary) {
	model, vocabulary, err := events.BuildGardenModel()
	if err != nil {
		panic(err)
	}
	return model, vocabulary
}

func encodeGardenSeedEvents(occurrences ...events.Occurrence) ([]store.BusEvent, error) {
	encoded := make([]store.BusEvent, len(occurrences))
	for i, occurrence := range occurrences {
		event, err := gardenSeedEventModel.Encode(occurrence)
		if err != nil {
			return nil, err
		}
		encoded[i] = store.BusEvent{
			Name: event.Name, Subject: event.Subject, Payload: string(event.Payload), Source: "garden",
		}
	}
	return encoded, nil
}

func announceGardenSeedEvents(d *Daemon, seqs []int64) {
	if len(seqs) == 0 || d == nil {
		return
	}
	d.queueConversationKeep()
	d.coalesceSnapshots(func() {
		if d.eventBus != nil {
			d.eventBus.Announce()
		}
		if d.gardenSeedEventConsumerStarted || d.store == nil {
			return
		}
		for _, seq := range seqs {
			rows, err := d.store.BusEventsSince(seq-1, 1)
			if err != nil || len(rows) != 1 || rows[0].Seq != seq {
				d.logf("Garden seed event %d committed but test-mode handling could not read it: rows=%d err=%v", seq, len(rows), err)
				continue
			}
			row := rows[0]
			if err := d.handleGardenSeedEventWithoutRoleLock(context.Background(), bus.Event{
				Seq: row.Seq, Name: row.Name, Subject: row.Subject, Payload: []byte(row.Payload),
				Source: row.Source, CreatedAt: row.CreatedAt,
			}); err != nil {
				d.logf("Garden seed event %d committed but test-mode handling failed: %v", seq, err)
			}
		}
	})
}

// lockGardenRoles resolves the bells of every seed event already committed, so no role change made
// under the lock reaches the audience of an earlier event; the durable consumer backstops a crash.
func (d *Daemon) lockGardenRoles() {
	d.gardenWatchMu.Lock()
	d.resolveCommittedGardenSeedBells()
}

func (d *Daemon) unlockGardenRoles() {
	d.gardenWatchMu.Unlock()
}

const gardenSeedBellBatch = 256

// resolveCommittedGardenSeedBells runs under the role lock. Receipts make each event's bells
// resolve once, whether here or in the durable consumer.
func (d *Daemon) resolveCommittedGardenSeedBells() {
	if d.store == nil {
		return
	}
	_, head, err := d.store.BusBounds()
	if err != nil {
		d.logf("Garden seed bells were left to the consumer: reading the event log bounds: %v", err)
		return
	}
	if head <= d.gardenBellsResolvedThrough {
		return
	}
	consumer, ok, err := d.store.GetBusConsumer(gardenSeedBellConsumer)
	if err != nil {
		d.logf("Garden seed bells were left to the consumer: reading its cursor: %v", err)
		return
	}
	// A paused consumer (`attn bus disable`) holds its bells until it is enabled again.
	if !ok || !consumer.Enabled {
		return
	}
	after := max(d.gardenBellsResolvedThrough, consumer.Cursor)
	for after < head {
		rows, err := d.store.BusEventsNamedBetween(after, head, "garden.seed.*", gardenSeedBellBatch)
		if err != nil {
			d.logf("Garden seed bells after event %d could not be read: %v", after, err)
			return
		}
		for _, row := range rows {
			if err := d.handleGardenSeedEventWithoutRoleLock(context.Background(), bus.Event{
				Seq: row.Seq, Name: row.Name, Subject: row.Subject, Payload: []byte(row.Payload),
				Source: row.Source, CreatedAt: row.CreatedAt,
			}); err != nil {
				d.logf("Garden seed event %d committed but its bells were left to the consumer: %v", row.Seq, err)
			}
		}
		if len(rows) < gardenSeedBellBatch {
			after = head
		} else {
			after = rows[len(rows)-1].Seq
		}
	}
	d.gardenBellsResolvedThrough = head
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func gardenSeedLifecycleOccurrence(verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	return gardenSeedLifecycleOccurrenceWithAttention(true, verb, seedID, causedBySessionID, directlyNotifiedSessionID...)
}

func quietGardenSeedLifecycleOccurrence(verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	return gardenSeedLifecycleOccurrenceWithAttention(false, verb, seedID, causedBySessionID, directlyNotifiedSessionID...)
}

func gardenSeedLifecycleOccurrenceWithAttention(attentionRequested bool, verb garden.Verb, seedID, causedBySessionID string, directlyNotifiedSessionID ...string) (events.Occurrence, error) {
	payload := events.LifecyclePayload{
		AttentionRequested:        attentionRequested,
		CausedBySessionID:         strings.TrimSpace(causedBySessionID),
		DirectlyNotifiedSessionID: firstString(directlyNotifiedSessionID),
	}
	switch verb {
	case garden.VerbTend:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Tended, seedID, payload)
	case garden.VerbPark:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Parked, seedID, payload)
	case garden.VerbHarvest:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Harvested, seedID, payload)
	case garden.VerbWither:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Withered, seedID, payload)
	case garden.VerbReplant:
		return events.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.Replanted, seedID, payload)
	default:
		return events.Occurrence{}, fmt.Errorf("no Garden seed lifecycle event is declared for %q", verb)
	}
}

func (d *Daemon) registerGardenSeedEventConsumer() error {
	return d.eventBus.RegisterWithPreDrain(
		gardenSeedBellConsumer,
		bus.Filter{"garden.seed.*"},
		func(_ context.Context, _ bus.Consumer, gap *bus.Gap) error {
			if gap == nil {
				return nil
			}
			return fmt.Errorf(
				"garden seed event history is incomplete: consumer cursor=%d, earliest=%d, head=%d, missed=%d",
				gap.Cursor, gap.Earliest, gap.Head, gap.Missed,
			)
		},
		d.handleGardenSeedEvent,
	)
}

func (d *Daemon) validatePendingGardenSeedBells() error {
	if d.store == nil {
		return nil
	}
	names, err := d.store.PendingGardenSeedBellNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := gardenSeedEventModel.ValidatePendingBell(name); err != nil {
			return fmt.Errorf("garden seed mailbox cannot start: %w", err)
		}
	}
	return nil
}

func (d *Daemon) handleGardenSeedEvent(_ context.Context, event bus.Event) error {
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	return d.handleGardenSeedEventWithoutRoleLock(context.Background(), event)
}

func (d *Daemon) handleGardenSeedEventWithoutRoleLock(_ context.Context, event bus.Event) error {
	decision, err := gardenSeedEventModel.Interpret(event.Name, event.Subject, event.Payload)
	if err != nil {
		return err
	}

	var recipients []string
	if !decision.Quiet() {
		resolver, err := d.readGardenEventRoles()
		if err != nil {
			return err
		}
		recipients, err = gardenSeedEventModel.Recipients(event.Subject, decision, resolver)
		if err != nil {
			return err
		}
	}
	deliveries := make([]store.GardenSeedBellDelivery, len(recipients))
	for i, recipient := range recipients {
		address, err := inbox.ParseAddress(recipient)
		if err != nil {
			return err
		}
		deliveries[i] = store.GardenSeedBellDelivery{To: address, ItemID: uuid.NewString()}
	}
	created, _, err := d.store.HandleGardenSeedEvent(
		event.Seq, event.Subject, strings.TrimPrefix(event.Name, "garden.seed."), decision.BellName(), deliveries, time.Now(),
	)
	if err != nil {
		return err
	}
	for _, address := range created {
		to, err := inbox.ParseAddress(address)
		if err != nil {
			return err
		}
		d.kickInboxAfterCommit(to)
	}
	return nil
}

type gardenEventRoles struct {
	daemon        *Daemon
	subscriptions gardenSubscriptions
}

func (d *Daemon) readGardenEventRoles() (gardenEventRoles, error) {
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return gardenEventRoles{}, err
	}
	return gardenEventRoles{daemon: d, subscriptions: subscriptions}, nil
}

func (r gardenEventRoles) ResolveSeedRole(seedID string, role events.Role) ([]string, error) {
	if _, exists := r.subscriptions.seeds[seedID]; !exists {
		schema, err := r.daemon.seedsCollection()
		if err != nil {
			return nil, fmt.Errorf("read seed %s for Garden bell eligibility: %w", seedID, err)
		}
		doc, found, err := r.daemon.store.GetDocument(*schema, seedID)
		if err != nil {
			return nil, fmt.Errorf("read seed %s for Garden bell eligibility: %w", seedID, err)
		}
		if !found {
			return nil, nil
		}
		if _, err := garden.Decode(doc.Body); err != nil {
			return nil, fmt.Errorf("read seed %s for Garden bell eligibility: %w", seedID, err)
		}
		return nil, fmt.Errorf("seed %s is absent from the Garden role snapshot", seedID)
	}
	switch role {
	case events.CurrentTender:
		seed, exists := r.subscriptions.seeds[seedID]
		if !exists {
			return nil, nil
		}
		if memberName := seed.Tender().Member; memberName != "" {
			member, found, err := r.daemon.resolveCrewMember(memberName)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, nil
			}
			return []string{inbox.ToMember(member.ID).String()}, nil
		}
		sessionID, err := r.daemon.localGardenTenderSession(seed.Tender())
		if errors.Is(err, errRemoteGardenTender) {
			return nil, nil
		}
		if err != nil || sessionID == "" {
			return nil, err
		}
		return []string{inbox.ToSession(sessionID).String()}, nil
	case events.CoveringWatchers:
		coverage, err := r.subscriptions.coverageChecked(seedID)
		if err != nil {
			return nil, err
		}
		sessions := make([]string, 0, len(coverage))
		for sessionID := range coverage {
			if r.daemon.store.Get(sessionID) != nil || r.daemon.store.DelegationSessionReserved(sessionID) {
				sessions = append(sessions, inbox.ToSession(sessionID).String())
			}
		}
		return sessions, nil
	default:
		return nil, fmt.Errorf("unsupported Garden seed audience role %d", role)
	}
}

func (d *Daemon) discardIneligibleGardenSeedBellsLocked(to inbox.Address) error {
	items, err := d.store.UnreadGardenSeedMailboxItems(to)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	resolver, err := d.readGardenEventRoles()
	if err != nil {
		return err
	}
	var discarded []string
	for _, item := range items {
		eligible, err := gardenSeedEventModel.RecipientEligible(item.BellName, item.SeedID, to.String(), resolver)
		if err != nil {
			return err
		}
		if !eligible {
			discarded = append(discarded, item.SeedID)
		}
	}
	for _, seedID := range discarded {
		if err := d.withdrawFromInbox(to, inbox.SeedUpdate, seedID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) discardAllIneligibleGardenSeedBellsLocked() error {
	items, err := d.store.PendingGardenSeedMailboxItems()
	if err != nil {
		return err
	}
	recipients := map[inbox.Address]bool{}
	for _, item := range items {
		recipients[item.To] = true
	}
	for sessionID := range recipients {
		if err := d.discardIneligibleGardenSeedBellsLocked(sessionID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) invalidateGardenSeedParties(reason string) {
	if d == nil || d.store == nil {
		return
	}
	d.lockGardenRoles()
	err := d.discardAllIneligibleGardenSeedBellsLocked()
	d.unlockGardenRoles()
	if err != nil {
		d.logf("Garden seed mailbox invalidation after %s: %v", reason, err)
	}
}

func (r gardenEventRoles) AddressesOfSession(sessionID string) []string {
	addresses := r.daemon.inboxRoleAddresses(sessionID)
	values := make([]string, len(addresses))
	for i, address := range addresses {
		values[i] = address.String()
	}
	return values
}
