package daemon

import (
	"context"

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
