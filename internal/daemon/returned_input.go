package daemon

import (
	"context"
	"sync"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type returnedInputs struct {
	mu      sync.Mutex
	queued  map[protocol.SessionID][]sessionmonitor.Delivery
	placing map[protocol.SessionID]bool
	again   map[protocol.SessionID]bool
}

func (d *Daemon) returnInput(session protocol.SessionID, id, text string) {
	r := &d.returned
	r.mu.Lock()
	if r.queued == nil {
		r.queued, r.placing, r.again = map[protocol.SessionID][]sessionmonitor.Delivery{}, map[protocol.SessionID]bool{}, map[protocol.SessionID]bool{}
	}
	r.queued[session] = append(r.queued[session], sessionmonitor.ReturnedUserInput(id, text))
	r.mu.Unlock()
	d.placeReturned(session)
}

func (d *Daemon) placeReturned(session protocol.SessionID) {
	r := &d.returned
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case len(r.queued[session]) == 0:
	case r.placing[session]:
		r.again[session] = true
	default:
		r.placing[session] = d.life.Go("placeReturned", func() { d.placeReturnedNow(session) })
	}
}

func (d *Daemon) placeReturnedNow(session protocol.SessionID) {
	r := &d.returned
	for {
		r.mu.Lock()
		delete(r.again, session)
		queue := r.queued[session]
		if len(queue) == 0 || d.store.Get(session) == nil {
			delete(r.queued, session)
			delete(r.placing, session)
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		head := queue[0]
		attempt := d.placeDelivery(context.Background(), session, head)
		if attempt.Stage == sessionmonitor.Placed {
			d.dropReturned(session, head)
			continue
		}
		if delay, retry := sessionmonitor.RetryDelay(attempt.Err); retry {
			d.life.AfterFunc("placeReturned", delay, func() { d.placeReturnedNow(session) })
			return
		}
		if d.stopPlacingReturned(session) {
			d.logf("returned input %s waits for session %s to change state: %v", head.ID, session, attempt.Err)
			pausepoint.At(pausepoint.ClaudeModReturnWaits)
			return
		}
	}
}

func (d *Daemon) stopPlacingReturned(session protocol.SessionID) bool {
	r := &d.returned
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.again[session] {
		return false
	}
	delete(r.placing, session)
	return true
}

func (d *Daemon) dropReturned(session protocol.SessionID, placed sessionmonitor.Delivery) {
	r := &d.returned
	r.mu.Lock()
	defer r.mu.Unlock()
	queue := r.queued[session]
	if len(queue) > 0 && queue[0].ID == placed.ID {
		queue = queue[1:]
	}
	if len(queue) == 0 {
		delete(r.queued, session)
		return
	}
	r.queued[session] = queue
}
