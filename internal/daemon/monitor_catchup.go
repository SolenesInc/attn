package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

func (h *monitorHome) catchUp(session protocol.SessionID, binding *monitorBinding, failure error) {
	if h.monitor == nil {
		return
	}
	d := h.daemon
	r := d.monitors()
	r.mu.Lock()
	defer r.mu.Unlock()
	if failure != nil {
		d.logf("monitor apply failed: session=%s: %v", session, failure)
	}
	if r.sessions[session] != binding {
		return
	}
	caughtUp := binding.applied.Revision.V >= h.monitor.Revision()
	if caughtUp || d.life.Ended() {
		binding.stopCatchUp()
		return
	}
	if binding.catchUp != nil {
		return
	}
	if binding.retryDelay == 0 {
		binding.retryDelay = jobs.DefaultBackoffBase
	} else {
		binding.retryDelay = min(binding.retryDelay*2, jobs.DefaultBackoffCap)
	}
	var timer *time.Timer
	timer = d.life.AfterFunc("monitorCatchUp", binding.retryDelay, func() {
		r.mu.Lock()
		if r.sessions[session] != binding || binding.catchUp != timer {
			r.mu.Unlock()
			return
		}
		binding.catchUp = nil
		r.mu.Unlock()
		h.monitor.Refresh()
	})
	binding.catchUp = timer
}

func (b *monitorBinding) stopCatchUp() {
	if b.catchUp != nil {
		b.catchUp.Stop()
		b.catchUp = nil
	}
	b.retryDelay = 0
}

func (d *Daemon) stopMonitorCatchUps() {
	r := d.monitors()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, binding := range r.sessions {
		binding.stopCatchUp()
	}
}
