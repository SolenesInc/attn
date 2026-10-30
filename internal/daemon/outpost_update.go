package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type outpostPush struct {
	cancel  context.CancelFunc
	done    chan struct{}
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
	d.startOutpostInstall(row, false)
}

func (d *Daemon) startOutpostInstall(row store.Outpost, add bool) {
	r := &d.outposts
	r.mu.Lock()
	if r.pushes == nil {
		r.pushes = make(map[string]*outpostPush)
	}
	if current := r.pushes[row.ID]; current != nil && current.running {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	push := &outpostPush{cancel: cancel, done: make(chan struct{}), running: true}
	r.pushes[row.ID] = push
	r.mu.Unlock()
	if !d.life.Go("installOutpost:"+row.ID, func() {
		defer close(push.done)
		defer cancel()
		err := d.pushOutpost(ctx, row, push, add)
		r.mu.Lock()
		push.running = false
		current := r.pushes[row.ID] == push
		r.mu.Unlock()
		if !current || ctx.Err() != nil {
			return
		}
		phase, outcome := "done", ""
		if err != nil {
			phase, outcome = "failed", err.Error()
		}
		d.setOutpostUpdate(row.ID, push, phase, outcome)
	}) {
		cancel()
		close(push.done)
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

func (d *Daemon) pushOutpost(ctx context.Context, row store.Outpost, push *outpostPush, add bool) error {
	remote := d.outpostSSH(row)
	err := remote.EnsureMaster(ctx)
	var probe outpostssh.Probe
	if err == nil {
		probe, err = remote.Probe(ctx)
	}
	self := d.bridgeIdentity().Build
	if err == nil && (!add || probe.Build == nil || *probe.Build != self) {
		err = d.installOutpost(ctx, remote, probe, func(phase string) { d.setOutpostUpdate(row.ID, push, phase, "") })
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if add {
			d.startOutpostDial(row)
		}
		return err
	}
	d.setOutpostUpdate(row.ID, push, "restarting", "")
	d.stopOutpostDial(row.ID)
	d.startOutpostDial(row)
	deadline, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		r := &d.outposts
		r.mu.Lock()
		run := r.dialers[row.ID]
		connected := run != nil && run.state == bridge.StateConnected && run.build == self.String()
		if r.changed == nil {
			r.changed = make(chan struct{})
		}
		changed := r.changed
		r.mu.Unlock()
		if connected {
			return nil
		}
		select {
		case <-changed:
		case <-deadline.Done():
			return fmt.Errorf("outpost did not reconnect on home build %s within 60 s: %w", self, deadline.Err())
		}
	}
}

func (d *Daemon) installOutpost(ctx context.Context, remote *outpostssh.Client, probe outpostssh.Probe, phase func(string)) error {
	path := ""
	if os.Getenv("ATTN_HARNESS_DATA_DIR") != "" {
		path = os.Getenv("ATTN_HARNESS_OUTPOST_BINARY")
	}
	if path == "" {
		var err error
		path, err = outpostbuild.Acquire(ctx, outpostbuild.Request{Build: d.bridgeIdentity().Build, Channel: buildinfo.Channel, Repository: buildinfo.RepositoryPath, ReleaseSource: buildinfo.ReleaseSource, Cache: filepath.Join(config.DataDir(), "outpost-builds"), Platform: probe.Platform}, phase)
		if err != nil {
			return err
		}
	}
	return remote.Install(ctx, path, d.bridgeIdentity().Build, phase)
}

func (d *Daemon) cancelOutpostUpdate(id string) {
	r := &d.outposts
	r.mu.Lock()
	push := r.pushes[id]
	if push != nil {
		push.cancel()
		delete(r.pushes, id)
	}
	r.mu.Unlock()
	if push != nil {
		<-push.done
	}
}
