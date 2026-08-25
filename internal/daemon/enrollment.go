package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

// The daemon's whole view of enrollment. It is re-read on every ask rather than cached,
// because `attn enrollment leave` and a home's sync both rewrite it under a live daemon.

func (d *Daemon) enrollmentStatus() (enrollment.Status, error) {
	return enrollment.Load(d.dataRoot)
}

func (d *Daemon) requireHome(surface string) error {
	status, err := d.enrollmentStatus()
	if err != nil {
		d.logf("enrollment: cannot read the record in %s: %v", d.dataRoot, err)
	}
	return status.RequireHome(surface)
}

// A daemon that is itself an outpost may not enroll anyone, so it passes the
// fence first and returns "" when refused.
func (d *Daemon) homeDaemonIDForEnrollment() string {
	if err := d.requireHome("enrolling another daemon as an outpost"); err != nil {
		d.logf("enrollment: %v", err)
		return ""
	}
	return d.daemonInstanceID
}

func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err != nil {
		return err
	}
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}
