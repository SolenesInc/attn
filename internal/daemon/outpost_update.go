package daemon

import (
	"context"
	"errors"
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
	cancel        context.CancelFunc
	done          chan struct{}
	state         protocol.OutpostUpdate
	running       bool
	installFailed bool
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
	busy := r.pushes[row.ID] != nil && r.pushes[row.ID].running
	r.mu.Unlock()
	if busy {
		return
	}
	if row.HostKey == "" {
		d.stopOutpostDial(row.ID)
		row, err = d.resolveOutpost(row.ID)
		if err != nil {
			d.sendCommandError(client, protocol.CmdUpdateOutpost, err.Error())
			return
		}
	}
	d.startOutpostInstall(row)
}

func (d *Daemon) startOutpostInstall(row store.Outpost) {
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
		defer cancel()
		d.finishOutpostPush(row.ID, push, d.pushOutpost(ctx, row, push))
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

func (d *Daemon) beginOutpostAdd(row store.Outpost, cancel context.CancelFunc) *outpostPush {
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pushes == nil {
		r.pushes = make(map[string]*outpostPush)
	}
	current := r.pushes[row.ID]
	if current != nil && (current.running || current.installFailed) {
		return nil
	}
	push := &outpostPush{cancel: cancel, done: make(chan struct{}), running: true}
	if current != nil {
		push.state = current.state
	}
	r.pushes[row.ID] = push
	return push
}

func (d *Daemon) finishOutpostPush(id string, push *outpostPush, err error) {
	r := &d.outposts
	r.mu.Lock()
	push.running = false
	r.mu.Unlock()
	phase, outcome := "done", ""
	if err != nil {
		phase, outcome = "failed", err.Error()
	}
	d.setOutpostUpdate(id, push, phase, outcome)
	close(push.done)
}

func (d *Daemon) prepareOutpost(ctx context.Context, row store.Outpost, push *outpostPush, force bool) (outpostRemote, error) {
	remote := d.outpostSSH(row)
	if err := remote.EnsureMaster(ctx); err != nil {
		return nil, err
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		var sshError *outpostssh.Error
		if errors.As(err, &sshError) {
			return nil, err
		}
		return nil, &bridge.Refusal{Reason: "platform", Detail: err.Error()}
	}
	different := probe.Build == nil || *probe.Build != d.bridgeIdentity().Build
	if push != nil && (force || different) {
		if err := d.installOutpost(ctx, remote, probe, func(phase string) { d.setOutpostUpdate(row.ID, push, phase, "") }); err != nil {
			d.outposts.mu.Lock()
			push.installFailed = true
			d.outposts.mu.Unlock()
			found := "nothing installed"
			var peer *bridge.Identity
			if probe.Build != nil {
				found = probe.Build.String()
				peer = &bridge.Identity{Build: *probe.Build}
			}
			return nil, &bridge.Refusal{Peer: peer, Reason: "install", Detail: fmt.Sprintf("%s; install failed: %v", found, err)}
		}
		d.setOutpostUpdate(row.ID, push, "restarting", "")
	} else if different {
		if probe.Build == nil {
			return nil, &bridge.Refusal{Reason: "build", Detail: "nothing installed; choose Update in Settings → Outposts at the home to install at " + remote.InstallPath()}
		}
		return nil, &bridge.Refusal{Peer: &bridge.Identity{Build: *probe.Build}, Reason: "build", Detail: fmt.Sprintf("outpost runs build %s; home runs %s; choose Update in Settings → Outposts at the home", *probe.Build, d.bridgeIdentity().Build)}
	}
	return remote, nil
}

func (d *Daemon) pushOutpost(ctx context.Context, row store.Outpost, push *outpostPush) error {
	if _, err := d.prepareOutpost(ctx, row, push, true); err != nil {
		if row.HostKey == "" && ctx.Err() == nil {
			d.startOutpostDial(row)
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	self := d.bridgeIdentity().Build
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

func (d *Daemon) installOutpost(ctx context.Context, remote outpostRemote, probe outpostssh.Probe, phase func(string)) error {
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
