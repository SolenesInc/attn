package daemon

import "github.com/victorarias/attn/internal/headless"

func (d *Daemon) headlessTaskRefused(caller string) bool {
	if headless.Enabled() {
		return false
	}
	d.logf("headless task refused (%s)", caller)
	return true
}
