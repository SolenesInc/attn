package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

func (d *Daemon) enrollmentStatus() (enrollment.Status, error) {
	return enrollment.Load(d.dataRoot)
}

func (d *Daemon) homeDaemonIDForEnrollment() string {
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
