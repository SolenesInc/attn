package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type outpostPush struct {
	cancel  context.CancelFunc
	state   protocol.OutpostUpdate
	running bool
}

func (d *Daemon) handleUpdateOutpost(client *wsClient, id string) {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	if !d.outpostsEnabled() {
		d.sendCommandError(client, protocol.CmdUpdateOutpost, "outposts_enabled is off")
		return
	}
	row, err := d.resolveOutpost(id)
	if err != nil {
		d.sendCommandError(client, protocol.CmdUpdateOutpost, err.Error())
		return
	}
	r.mu.Lock()
	if r.pushes == nil {
		r.pushes = make(map[string]*outpostPush)
	}
	if current := r.pushes[row.ID]; current != nil && current.running {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	push := &outpostPush{cancel: cancel, running: true}
	r.pushes[row.ID] = push
	r.mu.Unlock()
	if !d.life.Go("updateOutpost:"+row.ID, func() {
		defer cancel()
		err := d.pushOutpost(ctx, row, push)
		phase, outcome := "done", "Updated to the home's build."
		if err != nil {
			phase, outcome = "failed", err.Error()
		}
		r.mu.Lock()
		push.running = false
		r.mu.Unlock()
		d.setOutpostUpdate(row.ID, push, phase, outcome)
	}) {
		cancel()
	}
}

func (d *Daemon) setOutpostUpdate(id string, push *outpostPush, phase, outcome string) {
	r := &d.outposts
	r.mu.Lock()
	current := r.pushes[id] == push
	changed := push.state.Phase != protocol.OutpostUpdatePhase(phase) || protocol.Deref(push.state.Outcome) != outcome
	push.state = protocol.OutpostUpdate{Phase: protocol.OutpostUpdatePhase(phase)}
	if outcome != "" {
		push.state.Outcome = protocol.Ptr(outcome)
	}
	r.mu.Unlock()
	if current && changed {
		d.publishFact(FactOutpostChanged, id, nil)
	}
}

func (d *Daemon) pushOutpost(ctx context.Context, row store.Outpost, push *outpostPush) error {
	return fmt.Errorf("Update needs the ssh install, which is not available in this build; install the home's build by hand at %s", d.outpostSSH(row).InstallPath())
}

func (d *Daemon) cancelOutpostUpdate(id string) {
	r := &d.outposts
	r.mu.Lock()
	if push := r.pushes[id]; push != nil {
		push.cancel()
		delete(r.pushes, id)
	}
	r.mu.Unlock()
}
