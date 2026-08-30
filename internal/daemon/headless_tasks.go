package daemon

import (
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/jobs"
)

// headlessTaskRefused answers the one question every headless duty asks before
// it enqueues or runs, and leaves the receipt a harness reads back.
func (d *Daemon) headlessTaskRefused(caller string) bool {
	if headless.Enabled() {
		return false
	}
	d.logf("headless task refused (%s)", caller)
	return true
}

// headlessJobQueue is nil when the switch refuses the duty or the queue is down.
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
