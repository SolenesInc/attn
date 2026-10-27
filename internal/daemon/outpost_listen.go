package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
)

type outpostListener struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *bridge.Conn
}

type outpostRuntime struct {
	configure  sync.Mutex
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

func validateOutpostAddress(address string) error {
	if strings.TrimSpace(address) != address {
		return fmt.Errorf("address must not contain leading or trailing whitespace")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("expected an explicit address:port, received %q", address)
	}
	return nil
}

func (d *Daemon) configureOutpostListener() {
	r := &d.outpost
	r.configure.Lock()
	defer r.configure.Unlock()
	r.mu.Lock()
	old := r.listener
	r.listener = nil
	r.mu.Unlock()
	if old != nil {
		old.cancel()
		<-old.done
	}
	address := d.machineSetting(settingOutpostListen)
	if address == "" {
		d.setOutpostListenerState(nil, "off", nil)
		return
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &outpostListener{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.listener = run
	r.mu.Unlock()
	listen := r.listen
	if listen == nil {
		listen = net.Listen
	}
	listener, err := listen("tcp", address)
	if err != nil {
		cancel()
		close(run.done)
		d.setOutpostListenerState(run, "error", fmt.Errorf("outpost_listen %s: %w", address, err))
		return
	}
	d.setOutpostListenerState(run, "listening", nil)
	cfg := bridge.ServerConfig{
		Self: d.bridgeIdentity(), HostKeyPath: filepath.Join(d.dataRoot, "bridge_host_key"),
		AuthorizedKeys: func() ([]byte, error) { return os.ReadFile(config.AuthorizedKeysPath()) },
		Logf:           d.logf,
		Handlers:       d.outpostHandlers(),
		Update:         d.outpostUpdateServer(),
		Admit: func(home bridge.Identity) *bridge.Refusal {
			r.mu.Lock()
			status, err := enrollment.RecordHome(d.dataRoot, home.DaemonID, home.Name)
			if err == nil {
				r.enrollment = status
			}
			r.mu.Unlock()
			if err != nil {
				return &bridge.Refusal{Reason: "home", Detail: err.Error()}
			}
			return nil
		},
		OnError: func(err error) {
			state := "error"
			var refusal *bridge.Refusal
			if errors.As(err, &refusal) {
				state = "refused"
			}
			d.setOutpostListenerState(run, state, err)
		},
	}
	if !d.life.Go("outpostListener", func() {
		defer close(run.done)
		defer cancel()
		if err := bridge.Serve(ctx, listener, cfg, func(conn *bridge.Conn) { d.acceptOutpostBridge(run, conn) }); err != nil && ctx.Err() == nil {
			d.setOutpostListenerState(run, "error", err)
		}
	}) {
		_ = listener.Close()
		cancel()
		close(run.done)
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
	if !d.life.Go("outpostViews", func() { d.sendOutpostViews(d.life.Context(), conn) }) {
		_ = conn.Close()
		return
	}
	if !d.life.Go("outpostBridge", func() {
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
				d.updateOutpostListenerState(run, conn, "stalled", nil)
			case bridge.StateLost:
				d.updateOutpostListenerState(run, conn, "listening", conn.Err())
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
