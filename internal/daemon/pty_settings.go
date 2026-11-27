package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) newSharedPTYHost() (*ptybackend.WorkerBackend, error) {
	return ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{
		DataRoot:                 d.dataRoot,
		DaemonInstanceID:         d.daemonInstanceID,
		BinaryPath:               strings.TrimSpace(os.Getenv("ATTN_PTY_HOST_BINARY")),
		Logf:                     d.logf,
		OnTerminalBuild:          d.handleTerminalBuildChanged,
		OnSharedArtifactRejected: d.handleSharedArtifactRejected,
	})
}

const notificationKindPTYHostRejected = "pty_host_rejected"

func (d *Daemon) handleSharedArtifactRejected(rejection ptybackend.SharedArtifactRejection) error {
	impact := "New terminals cannot start until a working shared host is installed. " + d.sharedHostRemedy()
	if rejection.FallbackID != "" {
		impact = "New terminals keep using the last shared host build that passed."
	}
	if d.store == nil {
		return nil
	}
	record, created, err := d.store.EnsureNotification(store.NotificationRecord{
		ID:         notificationKindPTYHostRejected + ":" + rejection.ArtifactID,
		Kind:       notificationKindPTYHostRejected,
		Severity:   store.NotificationWarning,
		Title:      "Shared PTY host update failed its check",
		Body:       impact,
		Detail:     rejection.Source,
		Trigger:    "attn checked a newly installed shared PTY host with a throwaway terminal.",
		Impact:     impact,
		Cause:      rejection.Reason,
		SourceKind: "pty_host",
		SourceID:   rejection.ArtifactID,
	}, time.Now())
	if err != nil || !created {
		return err
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
	return nil
}

func (d *Daemon) validateSharedPTYHostAfterRecovery() {
	host := d.sharedPTYHost
	if host == nil || !shouldRunWorkerStartupProbe() {
		return
	}
	ctx, cancel := context.WithTimeout(d.life.Context(), workerStartupProbeTimeout)
	defer cancel()
	if err := host.ValidateSharedCandidate(ctx, false); err != nil {
		d.logf("shared PTY host candidate validation: %v", err)
	}
}

func (d *Daemon) spawnTerminal(ctx context.Context, opts ptybackend.SpawnOptions) error {
	return d.withSharedHostRemedy(d.ptyBackend.Spawn(ctx, opts))
}

func (d *Daemon) requireLaunchableHost(ctx context.Context) error {
	if d.sharedPTYHost == nil {
		return nil
	}
	return d.withSharedHostRemedy(d.sharedPTYHost.RequireLaunchableHost(ctx))
}

func (d *Daemon) withSharedHostRemedy(err error) error {
	if errors.Is(err, ptybackend.ErrSharedHostUnavailable) {
		return fmt.Errorf("%w. %s", err, d.sharedHostRemedy())
	}
	return err
}

func (d *Daemon) sharedHostRemedy() string {
	if d.enrollmentStatus().IsOutpost() {
		return "Update this outpost from its home, which installs attn-pty-host."
	}
	return "Reinstall attn to restore attn-pty-host."
}
