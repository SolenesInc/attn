package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err != nil {
		return err
	}
	d.outpost.mu.Lock()
	d.outpost.enrollment = status
	d.outpost.mu.Unlock()
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}

func (d *Daemon) enrollmentStatus() enrollment.Status {
	d.outpost.mu.Lock()
	defer d.outpost.mu.Unlock()
	return d.outpost.enrollment
}
