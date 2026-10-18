package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/store"
)

type outpostDial struct {
	cancel      context.CancelFunc
	done        chan struct{}
	state       bridge.State
	since       string
	build       string
	lastError   string
	lastErrorAt string
}

type outpostsRuntime struct {
	actions sync.Mutex
	mu      sync.Mutex
	dialers map[string]*outpostDial
}

func (d *Daemon) outpostsEnabled() bool {
	return parseBooleanSetting(d.daemonSetting(settingOutpostsEnabled))
}

func (d *Daemon) configureOutposts() {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	r.mu.Lock()
	old := r.dialers
	r.mu.Unlock()
	for _, run := range old {
		run.cancel()
	}
	for _, run := range old {
		<-run.done
	}
	r.mu.Lock()
	r.dialers = nil
	r.mu.Unlock()
	if !d.outpostsEnabled() {
		return
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		d.logf("list outposts: %v", err)
		return
	}
	d.coalesceSnapshots(func() {
		for _, row := range rows {
			d.startOutpostDial(row)
		}
	})
}

func (d *Daemon) startOutpostDial(row store.Outpost) {
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &outpostDial{cancel: cancel, done: make(chan struct{}), state: bridge.StateConnecting, since: time.Now().UTC().Format(time.RFC3339Nano)}
	r := &d.outposts
	r.mu.Lock()
	if r.dialers == nil {
		r.dialers = make(map[string]*outpostDial)
	}
	r.dialers[row.ID] = run
	r.mu.Unlock()
	if !d.life.Go("outpostDial:"+row.ID, func() { defer close(run.done); defer cancel(); d.dialOutpost(ctx, row, run) }) {
		cancel()
		close(run.done)
	}
}

func (d *Daemon) stopOutpostDial(id string) {
	r := &d.outposts
	r.mu.Lock()
	run := r.dialers[id]
	r.mu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	<-run.done
	r.mu.Lock()
	delete(r.dialers, id)
	r.mu.Unlock()
}

func (d *Daemon) updateOutpost(id string, run *outpostDial, state bridge.State, build, detail string) {
	r := &d.outposts
	r.mu.Lock()
	if r.dialers[id] != run {
		r.mu.Unlock()
		return
	}
	changed := run.state != state
	if changed {
		run.state, run.since = state, time.Now().UTC().Format(time.RFC3339Nano)
	}
	if build != "" && run.build != build {
		run.build = build
		changed = true
	}
	if state == bridge.StateConnected && run.lastError != "" {
		run.lastError, run.lastErrorAt = "", ""
		changed = true
	}
	if detail != "" && detail != run.lastError {
		run.lastError, run.lastErrorAt = detail, time.Now().UTC().Format(time.RFC3339Nano)
		changed = true
	}
	r.mu.Unlock()
	if changed {
		d.publishFact(FactOutpostChanged, id, nil)
	}
}

func (d *Daemon) dialOutpost(ctx context.Context, row store.Outpost, run *outpostDial) {
	for ctx.Err() == nil {
		d.updateOutpost(row.ID, run, bridge.StateConnecting, "", "")
		var pin *bridge.Pin
		if row.HostKey != "" {
			pin = &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}
		}
		conn, seen, err := bridge.Dial(ctx, bridge.DialConfig{Address: row.Address, Pin: pin, Self: d.bridgeIdentity(), AgentSocket: os.Getenv("SSH_AUTH_SOCK"), CheckHostKey: func(key string) *bridge.Refusal { return d.outpostConflict(row.ID, key, "") }})
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		if err == nil && pin == nil {
			err = d.store.PinOutpost(row.ID, seen.HostKey, seen.DaemonID)
			if err != nil {
				_ = conn.Close()
				if conflict := d.outpostConflict(row.ID, seen.HostKey, seen.DaemonID); conflict != nil {
					err = conflict
				} else {
					err = fmt.Errorf("pin outpost %s (%s): %w", row.Name, row.Address, err)
				}
			} else {
				row.HostKey, row.DaemonID = seen.HostKey, seen.DaemonID
				d.publishFact(FactOutpostChanged, row.ID, nil)
			}
		}
		if err != nil {
			state, delay := bridge.StateLost, bridge.ReconnectInterval
			var refusal *bridge.Refusal
			build := ""
			if errors.As(err, &refusal) {
				state, delay = bridge.StateRefused, bridge.LostAfter
				if refusal.Peer != nil {
					build = refusal.Peer.Build.String()
				}
			}
			d.updateOutpost(row.ID, run, state, build, err.Error())
			if !waitOutpostRetry(ctx, delay) {
				return
			}
			continue
		}
		for state := range conn.States() {
			if ctx.Err() != nil {
				_ = conn.Close()
				return
			}
			detail := ""
			if state == bridge.StateLost {
				detail = conn.Err().Error()
			}
			d.updateOutpost(row.ID, run, state, conn.Peer().Build.String(), detail)
		}
		_ = conn.Close()
	}
}

func waitOutpostRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (d *Daemon) outpostConflict(id, hostKey, daemonID string) *bridge.Refusal {
	rows, err := d.store.ListOutposts()
	if err != nil {
		return &bridge.Refusal{Reason: "identity", Detail: fmt.Sprintf("cannot check outpost identity against saved pins: %v", err)}
	}
	for _, row := range rows {
		if row.ID != id && (hostKey != "" && row.HostKey == hostKey || daemonID != "" && row.DaemonID == daemonID) {
			return &bridge.Refusal{Reason: "identity", Detail: fmt.Sprintf("same outpost as %s (%s), daemon %s; remove one of these outpost rows", row.Name, row.Address, row.DaemonID)}
		}
	}
	return nil
}
