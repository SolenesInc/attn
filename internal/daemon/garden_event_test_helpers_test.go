package daemon

import (
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/store"
	"time"
)

func markAutomationRunDeliveredForTest(s *store.Store, runID, resolved string, now time.Time) error {
	_, _, err := s.MarkAutomationRunDeliveredWithEvent(runID, resolved, store.BusEvent{
		Name: seedEvents.NameWorkReady, Subject: "s-test", Payload: `{"automation_run_id":"` + runID + `"}`,
	}, now)
	return err
}
