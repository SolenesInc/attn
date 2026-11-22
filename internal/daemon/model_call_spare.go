package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/stepclock"
)

const ModelCallSpareExpiryTimer stepclock.Name = "model-call-spare-expiry"

// The user's "several hours" (plot s-jhntry, D4): a constant, not a setting.
const modelCallsInUseFor = 4 * time.Hour

// modelCallSpare is the one owner of the spare: one harness process started
// ahead of any model call while model calls are in use.
type modelCallSpare struct {
	mu      sync.Mutex
	ready   *readySpare
	lastUse time.Time
	expiry  stepclock.Timer
}

type readySpare struct {
	harness string
	spare   harness.Spare
}

func (r *readySpare) Answer(ctx context.Context, request harness.OneShotRequest) (harness.OneShotResult, error) {
	defer r.Close()
	return r.spare.Answer(ctx, request)
}

func (r *readySpare) Close() { r.spare.Close() }

// used records that model calls are in use now: the user was present or a
// call arrived. It keeps or starts the spare and moves its expiry.
func (s *modelCallSpare) used(d *Daemon) {
	s.mu.Lock()
	s.lastUse = d.clock.Now()
	if s.expiry == nil {
		s.expiry = d.clock.AfterFunc(ModelCallSpareExpiryTimer, modelCallsInUseFor, func() {
			d.life.Go("modelCallSpareExpiry", d.checkModelCallSpare)
		})
	} else {
		s.expiry.Reset(modelCallsInUseFor)
	}
	keeps := s.ready != nil && s.ready.harness == d.modelCallDefaultHarness()
	s.mu.Unlock()
	if !keeps {
		d.checkModelCallSpare()
	}
}

// take hands the ready spare for harness to one call and starts the next.
func (s *modelCallSpare) take(d *Daemon, harness string) *readySpare {
	s.mu.Lock()
	taken := s.ready
	if taken != nil && taken.harness != harness {
		taken = nil
	}
	if taken != nil {
		s.ready = nil
	}
	s.mu.Unlock()
	if taken != nil {
		d.life.Go("modelCallSpareNext", d.checkModelCallSpare)
	}
	return taken
}

// checkModelCallSpare keeps one spare for the default harness while headless
// tasks are on, the harness can start ahead, a sender is paired and model
// calls are in use, and ends it otherwise.
func (d *Daemon) checkModelCallSpare() {
	s := &d.modelCallSpare
	s.mu.Lock()
	defer s.mu.Unlock()
	id, starter, why := d.spareWanted(s.lastUse)
	if s.ready != nil && starter != nil && s.ready.harness != id {
		why = "model calls now default to " + id
	}
	if s.ready != nil && (starter == nil || s.ready.harness != id) {
		d.logf("model call spare: ending the %s spare: %s", s.ready.harness, why)
		s.ready.Close()
		s.ready = nil
	}
	if starter == nil || s.ready != nil {
		return
	}
	spare, err := starter.StartSpare(d.life.Context())
	if err != nil {
		d.logf("model call spare: start %s, so calls run cold: %v", id, err)
		return
	}
	s.ready = &readySpare{harness: id, spare: spare}
}

func (d *Daemon) spareWanted(lastUse time.Time) (harness.ID, harness.SpareStarter, string) {
	id := d.modelCallDefaultHarness()
	if !headless.Enabled() {
		return id, nil, "headless tasks are off"
	}
	if lastUse.IsZero() || d.clock.Now().Sub(lastUse) >= modelCallsInUseFor {
		return id, nil, "no presence and no model call in 4 hours"
	}
	paired, err := d.store.HasPairedSenders()
	if err != nil {
		d.logf("model call spare: read paired senders: %v", err)
	}
	if !paired {
		return id, nil, "no sender is paired"
	}
	starter, ok := harness.As[harness.SpareStarter](d.harnessOf(id))
	if !ok {
		return id, nil, id + " cannot start ahead"
	}
	return id, starter, ""
}
