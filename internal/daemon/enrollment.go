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
	d.outpost.mu.Lock()
	d.installEnrollmentLocked(status)
	d.outpost.mu.Unlock()
	if _, err := d.adoptMachineID(status); err != nil {
		d.logf("enrollment: %v", err)
		d.addWarning(warnMachineIDNotAdopted, err.Error())
	}
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}

func (d *Daemon) installEnrollmentLocked(status enrollment.Status) {
	d.outpost.enrollment = status
}

func (d *Daemon) adoptMachineID(status enrollment.Status) (bool, error) {
	from := d.store.MachineID()
	if !status.IsOutpost() || status.MachineID == "" || status.MachineID == from {
		return false, nil
	}
	if err := d.store.AdoptMachineID(status.MachineID); err != nil {
		return false, fmt.Errorf("take the machine id %s that home %s knows this machine by: %w", status.MachineID, status.HomeID, err)
	}
	d.logf("enrollment: this machine is now %s, as its home knows it (was %s)", status.MachineID, from)
	return true, nil
}

func (d *Daemon) adoptMachineIDLive(status enrollment.Status) error {
	adopted, err := d.adoptMachineID(status)
	if err != nil || !adopted {
		return err
	}
	profiles, err := d.store.ListProfiles(false)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		d.publishArrangement(profile.ID, nil)
	}
	return nil
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
