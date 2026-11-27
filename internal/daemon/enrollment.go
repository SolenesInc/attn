package daemon

import (
	"fmt"

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

func (d *Daemon) adoptHomeID(status enrollment.Status) error {
	if err := d.store.AdoptCopySource(status.HomeDaemonID, status.HomeID); err != nil {
		return fmt.Errorf("store the copies from home %s under its home id %s: %w", status.HomeDaemonID, status.HomeID, err)
	}
	if err := d.adoptNotebookSyncHome(status.HomeDaemonID, status.HomeID); err != nil {
		return fmt.Errorf("key the Notebook sync state of home %s on its home id %s: %w", status.HomeDaemonID, status.HomeID, err)
	}
	adopted, err := enrollment.FinishAdoption(d.dataRoot)
	if err != nil {
		return err
	}
	d.outpost.mu.Lock()
	d.outpost.enrollment = adopted
	d.outpost.mu.Unlock()
	d.logf("enrollment: home %s is now %s", status.HomeDaemonID, status.HomeID)
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
