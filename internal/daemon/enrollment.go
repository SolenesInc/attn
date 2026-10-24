package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err != nil {
		return err
	}
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	if err := d.dropStaleCopy(); err != nil {
		d.logf("drop the copy of terminals run for another home: %v", err)
	}
	return nil
}
