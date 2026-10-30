package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type outpostDial struct {
	commandSession protocol.SessionID
	commandExit    chan int
	cancel         context.CancelFunc
	done           chan struct{}
	conn           *bridge.Conn
	state          bridge.State
	since          string
	build          string
	lastError      string
	lastErrorAt    string
	errorKind      string
	connectable    bool
	notebook       *protocol.OutpostNotebook
	notebookRoot   string
}

const maxOutpostRetryDelay = 128 * time.Second

type outpostRemote interface {
	EnsureMaster(context.Context) error
	ExitMaster(context.Context) error
	Probe(context.Context) (outpostssh.Probe, error)
	Bridge(context.Context) (net.Conn, error)
	Install(context.Context, string, bridge.Build, func(string)) error
	Remove(context.Context) error
	InstallPath() string
	MasterCommand() []string
}

type outpostsRuntime struct {
	actions sync.Mutex
	mu      sync.Mutex
	dialers map[string]*outpostDial
	pushes  map[string]*outpostPush
	changed chan struct{}
	remote  func(store.Outpost) outpostRemote
}

func (d *Daemon) outpostsEnabled() bool {
	return parseBooleanSetting(d.machineSetting(settingOutpostsEnabled))
}

func (d *Daemon) configureOutposts() {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	r.mu.Lock()
	for _, push := range r.pushes {
		push.cancel()
	}
	pushes := r.pushes
	r.pushes = nil
	r.mu.Unlock()
	for _, push := range pushes {
		<-push.done
	}
	r.mu.Lock()
	old := r.dialers
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
	if !enabled {
		for _, row := range rows {
			d.closeOutpostCommand(row.ID)
		}
	}
	d.coalesceSnapshots(func() {
		for _, row := range rows {
			if enabled {
				d.startOutpostDial(row)
			} else if err := d.outpostSSH(row).ExitMaster(d.life.Context()); err != nil {
				d.logf("stop outpost master %s: %v", row.Name, err)
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
		run.connectable = previous.connectable
		if previous.state == bridge.StateRefused {
			run.state, run.since = previous.state, previous.since
		}
	}
	commands, lookupErr := d.store.UnfinishedOutpostTerminals(row.ID)
	if len(commands) > 0 {
		run.commandSession = commands[0]
		run.commandExit = make(chan int, 1)
	}

	r.dialers[row.ID] = run
	r.mu.Unlock()
	if lookupErr != nil {
		d.updateOutpost(row.ID, run, bridge.StateRefused, "", fmt.Errorf("read connection terminals: %w", lookupErr))
		cancel()
		close(run.done)
		return
	}
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
	var sshError *outpostssh.Error
	connectable := errors.As(err, &sshError) && sshError.Connectable()
	changed := run.state != state || run.connectable != connectable
	run.connectable = connectable
	connected := changed && state == bridge.StateConnected
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
		if sshError != nil {
			run.lastError = sshError.Stderr
		}
		if kind := bridge.ErrorKind(err); kind != run.errorKind {
			run.errorKind, run.lastErrorAt = kind, time.Now().UTC().Format(time.RFC3339Nano)
			changed = true
		}
	}
	if changed {
		r.notifyChangedLocked()
	}
	r.mu.Unlock()
	if changed {
		d.publishFact(FactOutpostChanged, id, nil)
	}
	if connected {
		d.kickOutpostInboxes(id)
	}
}

func (r *outpostsRuntime) notifyChangedLocked() {
	if r.changed != nil {
		close(r.changed)
		r.changed = nil
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
	r.notifyChangedLocked()
	return true
}

func (d *Daemon) dialOutpost(ctx context.Context, row store.Outpost, run *outpostDial) {
	if run.commandExit != nil {
		select {
		case <-ctx.Done():
			return
		case code := <-run.commandExit:
			if code != 0 && !waitOutpostRetry(ctx, bridge.LostAfter) {
				return
			}
		}
	}
	retryDelay := bridge.ReconnectInterval
	for ctx.Err() == nil {
		var pin *bridge.Pin
		if row.HostKey != "" {
			pin = &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}
		}

		var push *outpostPush
		if row.HostKey == "" {
			push = d.beginOutpostAdd(row, run.cancel)
		}
		pipe, err := d.openOutpostPipe(ctx, row, push)
		var conn *bridge.Conn
		var seen *bridge.Pin
		if err == nil {
			conn, seen, err = bridge.Dial(ctx, bridge.DialConfig{Conn: pipe, Pin: pin, Self: d.bridgeIdentity(), Handlers: d.homeHandlers(row.ID), CheckHostKey: func(key string) *bridge.Refusal { return d.outpostConflict(row.ID, key, "") }})
		}

		if ctx.Err() != nil {
			if push != nil {
				d.finishOutpostPush(row.ID, push, ctx.Err())
			}
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		var buildRefusal *bridge.Refusal
		canPin := err == nil || errors.As(err, &buildRefusal) && buildRefusal.Reason == "build"
		if canPin && pin == nil && seen != nil {
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
					err = fmt.Errorf("pin outpost %s (%s): %w", row.Name, row.Destination, err)
				}
			} else {
				row.HostKey, row.DaemonID = seen.HostKey, seen.DaemonID
				d.configureNotebookSync()
				d.publishFact(FactOutpostChanged, row.ID, nil)
			}
		}
		if err != nil {
			state, delay := bridge.StateLost, retryDelay
			var refusal *bridge.Refusal
			build := ""
			var sshError *outpostssh.Error
			if errors.As(err, &sshError) && sshError.Connectable() {
				state, delay = bridge.StateRefused, bridge.LostAfter
			}
			if errors.As(err, &refusal) {
				state, delay = bridge.StateRefused, bridge.LostAfter
				if refusal.Peer != nil {
					build = refusal.Peer.Build.String()
				} else if refusal.Reason == "build" || refusal.Reason == "install" {
					build = "nothing installed"
				}
			}
			d.coalesceSnapshots(func() {
				if push != nil {
					d.finishOutpostPush(row.ID, push, err)
				}
				d.updateOutpost(row.ID, run, state, build, err)
			})
			if state == bridge.StateLost {
				retryDelay = min(retryDelay*2, maxOutpostRetryDelay)
			}
			if !waitOutpostRetry(ctx, delay) {
				return
			}
			continue
		}
		retryDelay = bridge.ReconnectInterval
		d.bridgeOutpost(ctx, row.ID, run, conn, push)
	}
}

func (d *Daemon) bridgeOutpost(ctx context.Context, id string, run *outpostDial, conn *bridge.Conn, push *outpostPush) {
	defer func() {
		if push != nil {
			err := conn.Err()
			if err == nil {
				err = fmt.Errorf("bridge closed before first pairing completed")
			}
			d.finishOutpostPush(id, push, err)
		}
	}()
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
		if state == bridge.StateConnected && push != nil {
			d.finishOutpostPush(id, push, nil)
			push = nil
		}
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
			return &bridge.Refusal{Reason: "identity", Detail: fmt.Sprintf("same outpost as %s (%s), daemon %s; remove one of these outpost rows", row.Name, row.Destination, row.DaemonID)}
		}
	}
	return nil
}

func (d *Daemon) outpostSSH(row store.Outpost) outpostRemote {
	if d.outposts.remote != nil {
		return d.outposts.remote(row)
	}
	return outpostssh.For(row.Destination, outpostssh.ControlPath(config.InstanceLabel(), row.ID), config.InstanceLabel())
}
func (d *Daemon) openOutpostPipe(ctx context.Context, row store.Outpost, push *outpostPush) (net.Conn, error) {
	remote, err := d.prepareOutpost(ctx, row, push, false)
	if err != nil {
		return nil, err
	}
	return remote.Bridge(ctx)
}
func validateOutpostDestination(destination string) error {
	if strings.TrimSpace(destination) != destination || destination == "" || strings.HasPrefix(destination, "-") || strings.ContainsAny(destination, "\r\n\x00") {
		return fmt.Errorf("ssh destination must be nonempty, must not start with a dash, and must not contain surrounding whitespace or control characters")
	}
	return nil
}
