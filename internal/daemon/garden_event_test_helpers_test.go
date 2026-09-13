package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/store"
)

const gardenRingUnblocked = "unblocked"

func markAutomationRunDeliveredForTest(s *store.Store, runID, resolved string, now time.Time) error {
	_, _, err := s.MarkAutomationRunDeliveredWithEvent(runID, resolved, store.BusEvent{
		Name: seedEvents.NameWorkReady, Subject: "s-test", Payload: `{"automation_run_id":"` + runID + `"}`,
	}, now)
	return err
}

func claimGardenSeedMailboxItemForTest(
	s *store.Store, recipientSessionID, seedID, eventName string, itemID string, now time.Time,
) (bool, error) {
	seq, err := s.AppendBusEvent(store.BusEvent{
		Name: seedEvents.NameUnblocked, Subject: seedID,
		Payload: `{"blocker_seed_id":"s-9k3f9m"}`, Source: "test",
	}, now)
	if err != nil {
		return false, err
	}
	created, _, err := s.HandleGardenSeedEvent(seq, seedID, eventName, seedEvents.BellSeedActivity, []store.GardenSeedBellDelivery{{
		RecipientSessionID: recipientSessionID, ItemID: itemID,
	}}, now)
	return len(created) == 1, err
}

func (d *Daemon) handleSeedEventForTest(name, seedID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	seq, err := d.store.AppendBusEvent(store.BusEvent{
		Name: name, Subject: seedID, Payload: string(raw), Source: "test",
	}, time.Now())
	if err != nil {
		return err
	}
	return d.handleGardenSeedEvent(context.Background(), bus.Event{
		Seq: seq, Name: name, Subject: seedID, Payload: raw, Source: "test",
	})
}

func (d *Daemon) ringSeedActivity(seedID, _ string, excludedSessionIDs ...string) {
	cause := firstString(excludedSessionIDs)
	_ = d.handleSeedEventForTest(seedEvents.NameNoteAdded, seedID, seedEvents.NoteAddedPayload{
		NoteID: "n-7k3f9m", AttentionRequested: true, CausedBySessionID: cause,
	})
}

func (d *Daemon) ringSeedUnblocked(unblocked []garden.Seed, excludedSessionIDs ...string) {
	cause := firstString(excludedSessionIDs)
	for _, seed := range unblocked {
		_ = d.handleSeedEventForTest(seedEvents.NameUnblocked, seed.ID, seedEvents.UnblockedPayload{
			BlockerSeedID: "s-9k3f9m", CausedBySessionID: cause,
		})
	}
}
