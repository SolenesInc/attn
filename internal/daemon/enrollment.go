package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/enrollment"
)

func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err == nil && status.IsHome() {
		status, err = enrollment.RecordMachineID(d.dataRoot, d.store.SelfMachineID())
	}
	if err != nil {
		return err
	}
	d.outpost.mu.Lock()
	d.installEnrollmentLocked(status)
	d.outpost.mu.Unlock()
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}

func (d *Daemon) installEnrollmentLocked(status enrollment.Status) {
	d.outpost.enrollment = status
	if status.IsOutpost() && status.MachineID != "" {
		d.store.SetMachineID(status.MachineID)
	} else {
		d.store.SetMachineID(d.store.SelfMachineID())
	}
}

func (d *Daemon) adoptHomeID(status enrollment.Status) error {
	copies, err := d.store.AdoptCopySource(status.HomeDaemonID, status.HomeID)
	if err != nil {
		return fmt.Errorf("store the copies from home %s under its home id %s: %w", status.HomeDaemonID, status.HomeID, err)
	}
	notebook, err := d.adoptNotebookSyncHome(status.HomeDaemonID, status.HomeID)
	if err != nil {
		return fmt.Errorf("key the Notebook sync state of home %s on its home id %s: %w", status.HomeDaemonID, status.HomeID, err)
	}
	if copies || notebook {
		d.logf("enrollment: state stored under home %s is now keyed on %s", status.HomeDaemonID, status.HomeID)
	}
	return nil
}

func (d *Daemon) enrollmentHome() enrollment.Home {
	self := d.bridgeIdentity()
	return enrollment.Home{ID: self.HomeID, DaemonID: self.DaemonID, Name: self.Name}
}

func (d *Daemon) enrollmentStatus() enrollment.Status {
	d.outpost.mu.Lock()
	defer d.outpost.mu.Unlock()
	return d.outpost.enrollment
}
