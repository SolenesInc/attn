package daemon

import (
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/jobs"
)

func (d *Daemon) headlessTaskRefused(caller string) bool {
	if headless.Enabled() {
		return false
	}
	d.logf("headless task refused (%s)", caller)
	return true
}

func (d *Daemon) headlessJobQueue(caller string) *jobs.Runner {
	if d.headlessTaskRefused(caller) {
		return nil
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return nil
	}
	return runner
}
