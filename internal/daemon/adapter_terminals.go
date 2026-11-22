package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	appServerTerminalOwner   = "appserver"
	codexSharedTerminalOwner = "codex-shared"
)

func (d *Daemon) adapterTerminal(t harness.TerminalID) bool {
	_, owned := d.adapterTerminalOwner(t)
	return owned
}

func (d *Daemon) adapterTerminalOwner(t harness.TerminalID) (string, bool) {
	if d.store == nil {
		return "", false
	}
	owner, owned, err := d.store.AdapterTerminalOwner(string(t))
	if err != nil {
		d.logf("read the owner of terminal %s: %v; keeping it as an adapter's", t, err)
		return "", true
	}
	return owner, owned
}

func (d *Daemon) startAdapterTerminal(ctx context.Context, owner string, opts ptybackend.SpawnOptions) error {
	if err := d.store.SaveAdapterTerminal(string(opts.ID), owner); err != nil {
		return err
	}
	if err := d.ptyBackend.Spawn(ctx, withLifecycleID(opts)); err != nil {
		if forgetErr := d.store.DeleteAdapterTerminal(string(opts.ID)); forgetErr != nil {
			d.logf("forget adapter terminal %s: %v", opts.ID, forgetErr)
		}
		return err
	}
	return nil
}

func (d *Daemon) adoptAdapterTerminal(ctx context.Context, owner string, t harness.TerminalID) {
	if _, live := d.liveTerminals(ctx)[t]; !live {
		return
	}
	if err := d.store.SaveAdapterTerminal(string(t), owner); err != nil {
		d.logf("record adapter terminal %s: %v", t, err)
	}
}

func (d *Daemon) forgetAdapterTerminal(t harness.TerminalID) {
	if d.store == nil {
		return
	}
	if err := d.store.DeleteAdapterTerminal(string(t)); err != nil {
		d.logf("forget adapter terminal %s: %v", t, err)
	}
}

func (d *Daemon) backfillLegacyServerTerminals(ctx context.Context, listingComplete bool) {
	pending, err := d.store.LegacyServerTerminalBackfillPending()
	if err != nil {
		d.logf("backfill legacy app-server terminals: %v", err)
		return
	}
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !pending || !ok {
		return
	}
	for terminal := range d.localLiveTerminals(ctx) {
		if !strings.HasSuffix(string(terminal), "-server") {
			continue
		}
		if info, err := provider.SessionInfo(ctx, terminal); err == nil && info.Agent == string(terminal) {
			if err := d.store.SaveAdapterTerminal(string(terminal), appServerTerminalOwner); err != nil {
				d.logf("backfill legacy app-server terminal %s: %v", terminal, err)
				return
			}
		}
	}
	if !listingComplete {
		return
	}
	if err := d.store.FinishLegacyServerTerminalBackfill(); err != nil {
		d.logf("finish the legacy app-server terminal backfill: %v", err)
	}
}

func (d *Daemon) sweepAdapterTerminals(ctx context.Context) {
	owned, err := d.store.AdapterTerminals()
	if err != nil {
		d.logf("sweep adapter terminals: %v", err)
		return
	}
	live := d.localLiveTerminals(ctx)
	for terminal := range owned {
		if _, running := live[harness.TerminalID(terminal)]; !running {
			d.forgetAdapterTerminal(harness.TerminalID(terminal))
		}
	}
}
