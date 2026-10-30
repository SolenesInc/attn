package daemon

import (
	"context"
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
)

type outpostListener struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *bridge.Conn
}

type outpostRuntime struct {
	mu         sync.Mutex
	enrollment enrollment.Status
	listener   *outpostListener
	state      string
	lastError  string
	errorKind  string
	listen     func(string, string) (net.Listener, error)
	updating   bool
}

func (d *Daemon) bridgeIdentity() bridge.Identity {
	name, _ := os.Hostname()
	return bridge.Identity{Name: name, Instance: config.InstanceLabel(), DaemonID: d.daemonInstanceID, Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
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
	cfg := bridge.ServerConfig{
		Self: d.bridgeIdentity(), HostKeyPath: filepath.Join(d.dataRoot, "bridge_host_key"),
		Logf:     d.logf,
		Handlers: d.outpostHandlers(),
		Update:   d.outpostUpdateServer(),
		Admit: func(home bridge.Identity) *bridge.Refusal {
			before := d.settingValues()
			r.mu.Lock()
			status, err := enrollment.RecordHome(d.dataRoot, home.DaemonID, home.Name)
			if err == nil {
				r.enrollment = status
			}
			r.mu.Unlock()
			if err != nil {
				return &bridge.Refusal{Reason: "home", Detail: err.Error()}
			}
			d.refreshChangedSettings(before)
			d.refreshJobQueueRole()
			return nil
		},
		OnError: func(err error) { d.logf("bridge: %v", err) },
	}
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
	errorChanged := false
	if state == "bridged" {
		errorChanged = r.lastError != ""
		r.lastError, r.errorKind = "", ""
	} else if err != nil {
		r.lastError = err.Error()
		kind := bridge.ErrorKind(err)
		errorChanged = kind != r.errorKind
		r.errorKind = kind
	}
	stateChanged := r.state != state
	r.state = state
	r.mu.Unlock()
	d.coalesceSnapshots(func() {
		if stateChanged {
			d.publishSettingsFact(FactSettingChanged, string(settingOutpostState))
		}
		if errorChanged {
			d.publishSettingsFact(FactSettingChanged, string(settingOutpostLastError))
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
			case bridge.StateStalled:
				d.updateOutpostListenerState(run, conn, "bridged", nil)
			case bridge.StateLost:
				d.updateOutpostListenerState(run, conn, "off", conn.Err())
			}
		}
	}) {
		_ = conn.Close()
	}
}

func (d *Daemon) outpostSettings() (state, home, lastError string) {
	r := &d.outpost
	r.mu.Lock()
	state, lastError = r.state, r.lastError
	if !r.enrollment.IsHome() {
		home = r.enrollment.HomeDaemonID
	}
	r.mu.Unlock()
	if state == "" {
		state = "off"
	}
	return state, home, lastError
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
