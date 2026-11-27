package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/enrollment"
)

func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err == nil && status.IsHome() {
		status, err = enrollment.RecordMachineID(d.dataRoot, d.store.MachineID())
	}
	if err != nil {
		return err
	}
	d.takeEnrolledMachineID(status)
	d.outpost.mu.Lock()
	d.outpost.enrollment = status
	d.outpost.mu.Unlock()
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}

func (d *Daemon) takeEnrolledMachineID(status enrollment.Status) {
	if status.IsOutpost() && status.MachineID != "" {
		d.store.SetMachineID(status.MachineID)
	}
}

func (d *Daemon) adoptHomeID(status enrollment.Status) error {
	if err := d.store.AdoptCopySource(status.Replaced, status.HomeID); err != nil {
		return fmt.Errorf("store the copies from home %s under its home id %s: %w", status.Replaced, status.HomeID, err)
	}
	if err := d.adoptNotebookSyncHome(status.Replaced, status.HomeID); err != nil {
		return fmt.Errorf("key the Notebook sync state of home %s on its home id %s: %w", status.Replaced, status.HomeID, err)
	}
	adopted, err := enrollment.FinishAdoption(d.dataRoot)
	if err != nil {
		return err
	}
	d.outpost.mu.Lock()
	d.outpost.enrollment = adopted
	d.outpost.mu.Unlock()
	d.logf("enrollment: home %s is now %s", status.Replaced, status.HomeID)
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
