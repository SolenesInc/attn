package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

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
