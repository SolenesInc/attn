package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type outpostDial struct {
	cancel       context.CancelFunc
	done         chan struct{}
	conn         *bridge.Conn
	state        bridge.State
	since        string
	build        string
	lastError    string
	lastErrorAt  string
	errorKind    string
	notebook     *protocol.OutpostNotebook
	notebookRoot string
}

type outpostsRuntime struct {
	actions sync.Mutex
	mu      sync.Mutex
	dialers map[string]*outpostDial
	pushes  map[string]*outpostPush
	changed chan struct{}
	dial    func(context.Context, string, string) (net.Conn, error)
}

func (d *Daemon) outpostsEnabled() bool {
	return parseBooleanSetting(d.machineSetting(settingOutpostsEnabled))
}

func (d *Daemon) configureOutposts() {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	r.mu.Lock()
	old := r.dialers
	for _, push := range r.pushes {
		push.cancel()
	}
	r.pushes = nil
	r.mu.Unlock()
	for _, run := range old {
		run.cancel()
	}
	for _, run := range old {
		<-run.done
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		d.logf("list outposts: %v", err)
		return
	}
	d.configureNotebookSync()
	enabled := d.outpostsEnabled()
	d.coalesceSnapshots(func() {
		for _, row := range rows {
			if enabled {
				d.startOutpostDial(row)
			}
			d.publishFact(FactOutpostChanged, row.ID, nil)
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
	if previous := r.dialers[row.ID]; previous != nil {
		run.build, run.lastError, run.lastErrorAt, run.errorKind = previous.build, previous.lastError, previous.lastErrorAt, previous.errorKind
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
}

func (d *Daemon) updateOutpost(id string, run *outpostDial, state bridge.State, build string, err error) {
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
		run.lastError, run.lastErrorAt, run.errorKind = "", "", ""
		changed = true
	}
	if err != nil {
		run.lastError = err.Error()
		if kind := bridge.ErrorKind(err); kind != run.errorKind {
			run.errorKind, run.lastErrorAt = kind, time.Now().UTC().Format(time.RFC3339Nano)
			changed = true
		}
	}
	if changed && r.changed != nil {
		close(r.changed)
		r.changed = nil
	}
	r.mu.Unlock()
	if changed {
		d.publishFact(FactOutpostChanged, id, nil)
	}
}

func (d *Daemon) setOutpostConn(id string, run *outpostDial, conn *bridge.Conn) bool {
	d.monitors().applyMu.Lock()
	defer d.monitors().applyMu.Unlock()
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dialers[id] != run {
		return false
	}
	run.conn = conn
	return true
}

func (d *Daemon) dialOutpost(ctx context.Context, row store.Outpost, run *outpostDial) {
	for ctx.Err() == nil {
		var pin *bridge.Pin
		if row.HostKey != "" {
			pin = &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}
		}
		conn, seen, err := bridge.Dial(ctx, bridge.DialConfig{Address: row.Address, Pin: pin, Self: d.bridgeIdentity(), AgentSocket: os.Getenv("SSH_AUTH_SOCK"), DialContext: d.outposts.dial, Handlers: d.homeHandlers(row.ID), CheckHostKey: func(key string) *bridge.Refusal { return d.outpostConflict(row.ID, key, "") }})
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		var buildRefusal *bridge.Refusal
		canPin := err == nil || errors.As(err, &buildRefusal) && buildRefusal.Reason == "build"
		if canPin && pin == nil {
			pinError := d.store.PinOutpost(row.ID, seen.HostKey, seen.DaemonID)
			if pinError != nil {
				err = pinError
			}
			if pinError != nil {
				if conn != nil {
					_ = conn.Close()
				}
				if conflict := d.outpostConflict(row.ID, seen.HostKey, seen.DaemonID); conflict != nil {
					err = conflict
				} else {
					err = fmt.Errorf("pin outpost %s (%s): %w", row.Name, row.Address, err)
				}
			} else {
				row.HostKey, row.DaemonID = seen.HostKey, seen.DaemonID
				d.configureNotebookSync()
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
			d.updateOutpost(row.ID, run, state, build, err)
			if !waitOutpostRetry(ctx, delay) {
				return
			}
			continue
		}
		d.bridgeOutpost(ctx, row.ID, run, conn)
	}
}

func (d *Daemon) bridgeOutpost(ctx context.Context, id string, run *outpostDial, conn *bridge.Conn) {
	defer func() { d.logf("outpost %s bridge closed: largest heartbeat gap %s", id, conn.MaxHeartbeatGap()) }()
	defer conn.Close()
	if !d.setOutpostConn(id, run, conn) {
		return
	}
	defer d.setOutpostConn(id, run, nil)
	d.life.Go("notebookStream:"+id, func() { d.openNotebookSync(conn) })
	sending := make(chan struct{})
	if !d.life.Go("outpostCopy:"+id, func() {
		defer close(sending)
		defer conn.Close()
		d.syncOutpost(ctx, id, conn)
	}) {
		return
	}
	defer func() { <-sending }()
	defer conn.Close()
	for state := range conn.States() {
		if ctx.Err() != nil {
			return
		}
		var err error
		if state == bridge.StateLost {
			err = conn.Err()
		}
		d.updateOutpost(id, run, state, conn.Peer().Build.String(), err)
	}
}

func (d *Daemon) syncOutpost(ctx context.Context, id string, conn *bridge.Conn) {
	if err := d.store.RequeueOutpostCopy(id); err != nil {
		d.logf("outpost %s requeue copy: %v", id, err)
		return
	}
	for {
		sent, err := d.sendOutpostCopyBatch(ctx, id, conn)
		if err != nil {
			d.logf("outpost %s initial copy: %v", id, err)
			return
		}
		if !sent {
			break
		}
	}
	if err := d.recoverOutpost(ctx, id, conn); err != nil {
		d.logf("outpost %s recovery: %v", id, err)
		return
	}
	theme := d.currentTerminalTheme()
	if err := conn.Call(ctx, bridgeCall, protocol.TerminalThemeMessage{Cmd: protocol.CmdTerminalTheme, Host: id, Foreground: theme.Foreground, Background: theme.Background, Cursor: theme.Cursor, AnsiPalette: theme.ANSIPalette[:]}, nil); err != nil {
		d.logf("outpost %s theme: %v", id, err)
	}
	d.sendOutpostCopy(ctx, id, conn)
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
