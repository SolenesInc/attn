package daemon

import (
	"context"
	"os"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
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
	spare   agentdriver.Spare
	workDir string
}

func (r *readySpare) Answer(ctx context.Context, request agentdriver.ModelCallRequest) (agentdriver.ModelCallResult, error) {
	request.WorkDir = r.workDir
	defer r.Close()
	return r.spare.Answer(ctx, request)
}

func (r *readySpare) Close() {
	r.spare.Close()
	_ = os.RemoveAll(r.workDir)
}

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
	harness, starter, why := d.spareWanted(s.lastUse)
	if s.ready != nil && starter != nil && s.ready.harness != harness {
		why = "model calls now default to " + harness
	}
	if s.ready != nil && (starter == nil || s.ready.harness != harness) {
		d.logf("model call spare: ending the %s spare: %s", s.ready.harness, why)
		s.ready.Close()
		s.ready = nil
	}
	if starter == nil || s.ready != nil {
		return
	}
	executable, err := d.machineAgentExecutable(harness, "")
	if err != nil {
		d.logf("model call spare: %s executable was not found, so calls run cold: %v", harness, err)
		return
	}
	workDir, err := os.MkdirTemp("", "attn-model-call-spare-*")
	if err != nil {
		d.logf("model call spare: create its scratch directory: %v", err)
		return
	}
	spare, err := starter.StartSpare(d.life.Context(), executable, workDir)
	if err != nil {
		_ = os.RemoveAll(workDir)
		d.logf("model call spare: start %s, so calls run cold: %v", harness, err)
		return
	}
	s.ready = &readySpare{harness: harness, spare: spare, workDir: workDir}
}

func (d *Daemon) spareWanted(lastUse time.Time) (string, agentdriver.SpareStarter, string) {
	harness := d.modelCallDefaultHarness()
	if !headless.Enabled() {
		return harness, nil, "headless tasks are off"
	}
	if lastUse.IsZero() || d.clock.Now().Sub(lastUse) >= modelCallsInUseFor {
		return harness, nil, "no presence and no model call in 4 hours"
	}
	paired, err := d.store.HasPairedSenders()
	if err != nil {
		d.logf("model call spare: read paired senders: %v", err)
	}
	if !paired {
		return harness, nil, "no sender is paired"
	}
	starter, ok := agentdriver.Get(harness).(agentdriver.SpareStarter)
	if !ok {
		return harness, nil, harness + " cannot start ahead"
	}
	return harness, starter, ""
}
