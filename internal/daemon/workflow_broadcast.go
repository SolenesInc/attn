package daemon

import (
	"context"
	"github.com/victorarias/attn/internal/bus"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// The daemon drops slow WebSocket clients past a 256-message buffer, so each tick broadcasts ONE full hydrated run: any single surviving frame carries the complete state.
const workflowBroadcastInterval = 75 * time.Millisecond

func (d *Daemon) markWorkflowRunDirty(runID string) {
	if d == nil || runID == "" {
		return
	}
	d.workflowBroadcastMu.Lock()
	defer d.workflowBroadcastMu.Unlock()
	if d.workflowDirty == nil {
		d.workflowDirty = make(map[string]bool)
	}
	d.workflowDirty[runID] = true
}

func (d *Daemon) flushWorkflowBroadcasts() {
	if d == nil {
		return
	}

	d.workflowBroadcastMu.Lock()
	if len(d.workflowDirty) == 0 {
		d.workflowBroadcastMu.Unlock()
		return
	}
	dirty := make([]string, 0, len(d.workflowDirty))
	for runID := range d.workflowDirty {
		dirty = append(dirty, runID)
	}
	d.workflowDirty = make(map[string]bool)
	d.workflowBroadcastMu.Unlock()

	for _, runID := range dirty {
		run, err := d.getWorkflowRunHydrated(runID)
		if err != nil {
			d.logf("workflow broadcast hydrate failed for run %s: %v", runID, err)
			continue
		}
		if run == nil {
			continue
		}
		d.publishFact(FactWorkflowRunUpdated, run.RunID, run)
	}
}

// Ships its own top-level event via BroadcastValue, not a WebSocketEvent field; the optional hook exists because the wsHub's WebSocketEvent-only broadcastListener cannot see this message type.
func (d *Daemon) projectWorkflowRunUpdated(ev bus.Event) {
	run, ok := decodeFact[*protocol.WorkflowRun](d, ev)
	if !ok || run == nil {
		return
	}
	msg := &protocol.WorkflowRunUpdatedMessage{
		Event: protocol.EventWorkflowRunUpdated,
		Run:   *run,
	}
	if d.workflowBroadcastHook != nil {
		d.workflowBroadcastHook(msg)
	}
	if d.wsHub != nil {
		d.wsHub.BroadcastValue(msg)
	}
}

func (d *Daemon) startWorkflowBroadcastLoop(ctx context.Context) {
	if d == nil {
		return
	}
	ticker := time.NewTicker(workflowBroadcastInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.flushWorkflowBroadcasts()
		}
	}
}
