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
	configure sync.Mutex
	mu        sync.Mutex
	listener  *outpostListener
	state     string
	lastError string
}

func (d *Daemon) bridgeIdentity() bridge.Identity {
	return bridge.Identity{DaemonID: d.daemonInstanceID, Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
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
	address := d.daemonSetting(settingOutpostListen)
	if address == "" {
		d.setOutpostListenerState(nil, "off", "")
		return
	}
	ctx, cancel := context.WithCancel(d.life.Context())
	run := &outpostListener{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.listener = run
	r.mu.Unlock()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		cancel()
		close(run.done)
		d.setOutpostListenerState(run, "error", fmt.Sprintf("outpost_listen %s: %v", address, err))
		return
	}
	d.setOutpostListenerState(run, "listening", "")
	cfg := bridge.ServerConfig{
		Self: d.bridgeIdentity(), HostKeyPath: filepath.Join(d.dataRoot, "bridge_host_key"),
		AuthorizedKeys: func() ([]byte, error) { return os.ReadFile(config.AuthorizedKeysPath()) },
		Logf:           d.logf,
		Admit: func(home bridge.Identity) *bridge.Refusal {
			if _, err := enrollment.RecordHome(d.dataRoot, home.DaemonID); err != nil {
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
			d.setOutpostListenerState(run, state, err.Error())
		},
	}
	if !d.life.Go("outpostListener", func() {
		defer close(run.done)
		defer cancel()
		if err := bridge.Serve(ctx, listener, cfg, func(conn *bridge.Conn) { d.acceptOutpostBridge(run, conn) }); err != nil && ctx.Err() == nil {
			d.setOutpostListenerState(run, "error", err.Error())
		}
	}) {
		_ = listener.Close()
		cancel()
		close(run.done)
	}
}

func (d *Daemon) setOutpostListenerState(run *outpostListener, state, detail string) {
	d.updateOutpostListenerState(run, nil, state, detail)
}

func (d *Daemon) updateOutpostListenerState(run *outpostListener, conn *bridge.Conn, state, detail string) {
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
	lastError := r.lastError
	if state == "bridged" {
		lastError = ""
	} else if detail != "" {
		lastError = detail
	}
	stateChanged, errorChanged := r.state != state, r.lastError != lastError
	r.state, r.lastError = state, lastError
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
				d.updateOutpostListenerState(run, conn, "bridged", "")
			case bridge.StateStalled:
				d.updateOutpostListenerState(run, conn, "stalled", "")
			case bridge.StateLost:
				d.updateOutpostListenerState(run, conn, "listening", conn.Err().Error())
			}
		}
	}) {
		_ = conn.Close()
	}
}

func (d *Daemon) outpostSettings() (state, home, lastError string) {
	r := &d.outpost
	r.mu.Lock()
	state, lastError, active := r.state, r.lastError, r.listener != nil
	r.mu.Unlock()
	if state == "" {
		state = "off"
	}
	if active {
		if status, err := enrollment.Load(d.dataRoot); err == nil && !status.IsHome() {
			home = status.HomeDaemonID
		}
	}
	return state, home, lastError
}
