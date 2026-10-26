package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/outpostbuild"
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
	phase := func(value string) { d.setOutpostUpdate(row.ID, push, value, "") }
	if buildinfo.Channel != "source" && buildinfo.Channel != "release" {
		return fmt.Errorf("home install channel %q cannot provide an outpost build; need source or release", buildinfo.Channel)
	}
	phase(map[string]string{"source": "building", "release": "downloading"}[buildinfo.Channel])
	var pin *bridge.Pin
	if row.HostKey != "" {
		pin = &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}
	}
	conn, seen, err := bridge.DialUpdate(ctx, bridge.DialConfig{Address: row.Address, Pin: pin, Self: d.bridgeIdentity(), AgentSocket: os.Getenv("SSH_AUTH_SOCK"), DialContext: d.outposts.dial, CheckHostKey: func(key string) *bridge.Refusal { return d.outpostConflict(row.ID, key, "") }})
	if err != nil {
		return err
	}
	defer conn.Close()
	if pin == nil {
		if err := d.store.PinOutpost(row.ID, seen.HostKey, seen.DaemonID); err != nil {
			return err
		}
		row.HostKey, row.DaemonID = seen.HostKey, seen.DaemonID
	}
	build := d.bridgeIdentity().Build
	if conn.Peer().Build == build {
		return nil
	}
	var binary string
	if os.Getenv("ATTN_HARNESS_DATA_DIR") != "" && buildinfo.Channel == "source" && os.Getenv("ATTN_HARNESS_OUTPOST_BINARY") != "" {
		if conn.Target().Platform != runtime.GOOS+"/"+runtime.GOARCH {
			return fmt.Errorf("test source strategy cannot supply platform %s", conn.Target().Platform)
		}
		binary = os.Getenv("ATTN_HARNESS_OUTPOST_BINARY")
	} else {
		binary, err = outpostbuild.Acquire(ctx, outpostbuild.Request{Build: build, Channel: buildinfo.Channel, Repository: buildinfo.RepositoryPath, ReleaseSource: buildinfo.ReleaseSource, Cache: filepath.Join(d.dataRoot, "outpost-builds"), Platform: conn.Target().Platform}, phase)
		if err != nil {
			return err
		}
	}
	if err := conn.Send(ctx, build, binary, phase); err != nil {
		return err
	}
	r := &d.outposts
	r.actions.Lock()
	if ctx.Err() != nil {
		r.actions.Unlock()
		return ctx.Err()
	}
	d.stopOutpostDial(row.ID)
	d.startOutpostDial(row)
	r.actions.Unlock()
	deadline, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		r.mu.Lock()
		run := r.dialers[row.ID]
		connected := run != nil && run.state == bridge.StateConnected && run.build == build.String()
		lastError := ""
		if run != nil {
			lastError = run.lastError
		}
		if r.changed == nil {
			r.changed = make(chan struct{})
		}
		changed := r.changed
		r.mu.Unlock()
		if connected {
			return nil
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("outpost %s did not return connected on build %s within 60s: %s: %w", row.Name, build, lastError, deadline.Err())
		case <-changed:
		}
	}
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
