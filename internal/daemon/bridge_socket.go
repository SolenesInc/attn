package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

type outpostListener struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *bridge.Conn
}

type outpostRuntime struct {
	mu          sync.Mutex
	enrollment  enrollment.Status
	listener    *outpostListener
	state       string
	lastError   string
	errorKind   string
	refusal     string
	listen      func(string, string) (net.Listener, error)
	dial        *homeDial
	changed     chan struct{}
	servesApps  chan struct{}
	refusedBy   *bridge.Identity
	homeBridged chan struct{}
}

// homeStateChanges is closed at the next change of this machine's bridge to its home, or of the reason it gives.
func (d *Daemon) homeStateChanges() <-chan struct{} {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.changed == nil {
		r.changed = make(chan struct{})
	}
	return r.changed
}

func (r *outpostRuntime) wakeHomeWaitersLocked() {
	if r.changed != nil {
		close(r.changed)
		r.changed = nil
	}
}

// homeServesAppsAgain is closed when a copy from the home says it serves apps on other machines.
func (d *Daemon) homeServesAppsAgain() <-chan struct{} {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.servesApps == nil {
		r.servesApps = make(chan struct{})
	}
	return r.servesApps
}

func (d *Daemon) homeServesApps() {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.servesApps != nil {
		close(r.servesApps)
		r.servesApps = nil
	}
}

func (d *Daemon) bridgeIdentity() bridge.Identity {
	name, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return bridge.Identity{Name: name, Instance: config.InstanceLabel(), DaemonID: d.daemonInstanceID, Home: home, HomeID: d.enrollmentStatus().HomeID, MachineID: d.store.MachineID(), Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
}

func (d *Daemon) startBridgeSocket() error {
	r := &d.outpost
	defer d.configureNotebookSync()
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &outpostListener{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.listener = run
	r.mu.Unlock()
	listen := r.listen
	if listen == nil {
		listen = listenBridgeSocket
	}
	listener, err := listen("unix", filepath.Join(d.dataRoot, "bridge.sock"))
	if err != nil {
		cancel()
		close(run.done)
		return err
	}
	d.setOutpostListenerState(run, "off", nil)
	cfg := d.outpostServerConfig("")
	if !d.life.Go("outpostListener", func() {
		defer close(run.done)
		defer cancel()
		if err := bridge.Serve(ctx, listener, cfg, func(conn *bridge.Conn) { d.acceptOutpostBridge(run, conn) }); err != nil && ctx.Err() == nil {
			d.logf("bridge socket: %v", err)
		}
	}) {
		_ = listener.Close()
		cancel()
		close(run.done)
	}
	return nil
}

// outpostServerConfig serves a home's bridge to this machine; dial is the destination this machine dials the home
// by, empty when the home dials.
func (d *Daemon) outpostServerConfig(dial string) bridge.ServerConfig {
	r := &d.outpost
	return bridge.ServerConfig{
		Self: d.bridgeIdentity, HostKeyPath: filepath.Join(d.dataRoot, "bridge_host_key"),
		Logf:     d.logf,
		Handlers: d.outpostHandlers(),
		Admit: func(home bridge.Identity) *bridge.Refusal {
			if dial != "" && home.DaemonID == d.daemonInstanceID {
				return &bridge.Refusal{Reason: "self", Detail: fmt.Sprintf("%s is this machine's own daemon; join a home on another machine", dial)}
			}
			if refusal := d.homeMoveAdmit(home); refusal != nil {
				return refusal
			}
			if refusal := d.ownSessionsRefusal(home); dial != "" && refusal != nil {
				return refusal
			}
			before := d.settingValues()
			r.mu.Lock()
			tookHomeID := r.enrollment.AwaitsHomeID()
			status, err := enrollment.RecordHome(d.dataRoot, enrollment.Home{ID: home.HomeID, DaemonID: home.DaemonID, Name: home.Name, MachineID: home.MachineID, PeerMachineID: home.PeerMachineID}, dial)
			if err == nil && !tookHomeID {
				d.installEnrollmentLocked(status)
			}
			r.mu.Unlock()
			if err == nil && tookHomeID {
				if err = d.adoptHomeID(status); err == nil {
					r.mu.Lock()
					d.installEnrollmentLocked(status)
					r.mu.Unlock()
				}
			}
			if err == nil {
				err = d.adoptMachineIDLive(status)
			}
			if err != nil {
				return &bridge.Refusal{Reason: "home", Detail: err.Error()}
			}
			d.refreshChangedSettings(before)
			d.refreshJobQueueRole()
			return nil
		},
		OnError: func(err error) { d.logf("bridge: %v", err) },
	}
}

func (d *Daemon) setOutpostListenerState(run *outpostListener, state string, err error) {
	d.updateOutpostListenerState(run, nil, state, err)
}

func (d *Daemon) updateOutpostListenerState(run *outpostListener, conn *bridge.Conn, state string, err error) {
	r := &d.outpost
	r.mu.Lock()
	if r.listener != run || conn != nil && run.conn != conn {
		r.mu.Unlock()
		return
	}
	if conn == nil && run != nil && run.conn != nil {
		select {
		case <-run.conn.Done():
		default:
			state = r.state
		}
	}
	errorChanged, before := false, r.lastError
	if state == "bridged" {
		errorChanged = r.lastError != ""
		r.lastError, r.errorKind, r.refusal = "", "", ""
	} else if err != nil {
		r.lastError = err.Error()
		r.refusal = refusalReason(err)
		kind := bridge.ErrorKind(err)
		errorChanged = kind != r.errorKind
		r.errorKind = kind
	}
	stateChanged := r.state != state
	r.state = state
	if stateChanged || errorChanged || err != nil && r.lastError != before {
		r.wakeHomeWaitersLocked()
	}
	r.mu.Unlock()
	d.coalesceSnapshots(func() {
		if stateChanged {
			d.publishFact(FactSettingChanged, setting.OutpostState.Key, nil)
		}
		if errorChanged {
			d.publishFact(FactSettingChanged, setting.OutpostLastError.Key, nil)
		}
	})
}

func (d *Daemon) acceptOutpostBridge(run *outpostListener, conn *bridge.Conn) {
	pausepoint.At(pausepoint.OutpostBridgeAccept)
	r := &d.outpost
	r.mu.Lock()
	if r.listener != run {
		r.mu.Unlock()
		_ = conn.Close()
		return
	}
	previous := run.conn
	run.conn = conn
	r.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	d.finishMoveOnBridge(conn.Peer().DaemonID)
	d.store.StartCopyOrder()
	d.configureNotebookSync()
	if !d.life.Go("outpostViews", func() { d.sendOutpostViews(d.life.Context(), conn) }) {
		_ = conn.Close()
		return
	}
	if !d.life.Go("outpostBridge", func() {
		defer func() { d.logf("home bridge closed: largest heartbeat gap %s", conn.MaxHeartbeatGap()) }()
		for state := range conn.States() {
			r.mu.Lock()
			current := r.listener == run && run.conn == conn
			r.mu.Unlock()
			if !current {
				return
			}
			switch state {
			case bridge.StateConnected:
				d.updateOutpostListenerState(run, conn, "bridged", nil)
				if d.relaysApps() {
					d.sendLocalAppsToHome()
				}
				d.clearHomeUpdateNotice()
				d.queueAnalyticsForward()
				d.retryPendingAdoptions()
			case bridge.StateStalled:
				d.updateOutpostListenerState(run, conn, "bridged", nil)
			case bridge.StateLost:
				d.updateOutpostListenerState(run, conn, d.homeLostState(), conn.Err())
				d.forgetHomeTileDocuments(run, conn)
			}
		}
	}) {
		_ = conn.Close()
	}
}

func (d *Daemon) forgetHomeTileDocuments(run *outpostListener, conn *bridge.Conn) {
	w, r := &d.tileDocuments, &d.outpost
	w.mu.Lock()
	r.mu.Lock()
	current := r.listener == run && run.conn == conn
	r.mu.Unlock()
	if !current {
		w.mu.Unlock()
		return
	}
	forgotten := w.watched
	w.watched = map[string]*watchedTileDocument{}
	w.mu.Unlock()
	for path := range forgotten {
		d.releaseCodeNavigation(path)
	}
}

func (d *Daemon) outpostSettings() (state, home, lastError string) {
	r := &d.outpost
	r.mu.Lock()
	state, lastError = r.state, r.lastError
	if r.enrollment.IsOutpost() {
		home = r.enrollment.CopiedFrom()
	}
	r.mu.Unlock()
	if state == "" {
		state = "off"
	}
	return state, home, lastError
}

func (d *Daemon) awayFromHome(sessionID protocol.SessionID) bool {
	if !d.monitors().copied(sessionID) {
		return false
	}
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state != "bridged"
}

func listenBridgeSocket(network, path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen(network, path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
