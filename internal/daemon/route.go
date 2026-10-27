package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/settingkeys"
	"github.com/victorarias/attn/internal/store"
)

type machinePart struct {
	readOnly bool
	run      func(context.Context, protocol.Placed) (any, error)
	decode   func([]byte) (protocol.Placed, error)
}

type Router struct {
	daemon *Daemon
	here   map[string]machinePart
}

func (d *Daemon) router() *Router {
	d.routerOnce.Do(func() { d.routes = &Router{daemon: d, here: d.machineParts()} })
	return d.routes
}

func Call[R any](ctx context.Context, r *Router, msg protocol.Placed) (R, error) {
	var answer R
	conn, err := r.bridge(msg)
	if err != nil {
		return answer, err
	}
	if conn != nil {
		err = conn.Call(ctx, bridgeCall, msg, &answer)
		return answer, terminalBridgeError(err)
	}
	part, ok := r.here[msg.CommandName()]
	if !ok {
		return answer, fmt.Errorf("no machine part runs %s", msg.CommandName())
	}
	result, err := part.run(ctx, msg)
	if err != nil {
		return answer, err
	}
	answer, ok = result.(R)
	if !ok {
		return answer, fmt.Errorf("%s answers %T, not %T", msg.CommandName(), result, answer)
	}
	return answer, nil
}

type Origin struct {
	Host, Name string
	Caller     protocol.SessionID
	bridge     *bridge.Conn
}

type originKey struct{}

func OriginOf(ctx context.Context) (Origin, bool) {
	origin, ok := ctx.Value(originKey{}).(Origin)
	return origin, ok
}

func (r *Router) bridge(msg protocol.Placed) (*bridge.Conn, error) {
	d := r.daemon
	var host string
	var home bool
	switch msg.Placement().Locator {
	case protocol.LocatorHere:
		if request, ok := msg.(protocol.GetSettingsMessage); ok {
			home = protocol.Deref(request.ProfileID) != "" || protocol.Deref(request.SourceSessionID) != ""
			if key := protocol.Deref(request.Key); key == string(SettingNotebookRootEffective) || key == string(settingNotebookSyncState) || key == string(settingNotebookSyncLastError) {
				home = false
			}
		}
	case protocol.LocatorHome:
		home = true
	case protocol.LocatorSetting:
		spec, err := writableSetting(msg.Locate())
		if err != nil {
			return nil, err
		}
		home = spec.class == settingkeys.Home
		if request, ok := msg.(protocol.SetSettingMessage); ok && spec.scope == profileScope {
			home = protocol.Deref(request.ProfileID) != "" || protocol.Deref(request.SourceSessionID) != ""
		}
	case protocol.LocatorMachine:
		host = msg.Locate()
	case protocol.LocatorSession:
		id := protocol.SessionID(msg.Locate())
		_, copied := d.store.SessionHost(id)
		home = !copied
		host = d.sessionHost(id)
	case protocol.LocatorTerminal:
		session := d.terminals().owner(harness.TerminalID(msg.Locate()))
		_, copied := d.store.SessionHost(session)
		home = !copied
		host = d.sessionHost(session)
	}
	if home {
		status := d.enrollmentStatus()
		if !status.IsHome() {
			conn, _, err := d.homeConn(status)
			return conn, err
		}
	}
	if host == "" {
		return nil, nil
	}
	conn, _, err := d.outpostConn(host)
	return conn, err
}

func (d *Daemon) homeConn(status enrollment.Status) (*bridge.Conn, string, error) {
	name := status.HomeName
	if name == "" {
		name = status.HomeDaemonID
	}
	if conn := d.homeBridge(); conn != nil {
		return conn, name, nil
	}
	return nil, name, fmt.Errorf("%s is unreachable; this needs it now", name)
}

func (d *Daemon) sessionHost(id protocol.SessionID) string {
	if id == "" {
		return ""
	}
	host, copied := d.store.SessionHost(id)
	if copied {
		return ""
	}
	return host
}

func (d *Daemon) localDirectory(session *protocol.Session) string {
	if d.sessionHost(session.ID) != "" {
		return ""
	}
	return session.Directory
}

func (d *Daemon) outpostPathRefusal(path, host string) string {
	name := host
	if row, err := d.resolveOutpost(host); err == nil {
		name = row.Name
	}
	return fmt.Sprintf("%s is on %s; opening files on an outpost isn't supported yet", path, name)
}

func (d *Daemon) resolveOutpost(host string) (store.Outpost, error) {
	rows, err := d.store.ListOutposts()
	if err != nil {
		return store.Outpost{}, err
	}
	for _, row := range rows {
		if row.ID == host || row.Name == host {
			return row, nil
		}
	}
	return store.Outpost{}, fmt.Errorf("outpost %q does not exist", host)
}

func (d *Daemon) outpostConn(host string) (*bridge.Conn, string, error) {
	row, err := d.resolveOutpost(host)
	if err != nil {
		return nil, host, err
	}
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	if conn, reach := outpostReach(d.outpostsEnabled(), r.dialers[row.ID]); reach == protocol.OutpostReachReachable {
		return conn, row.Name, nil
	}
	return nil, row.Name, fmt.Errorf("%s is unreachable; this needs it now", row.Name)
}

func outpostReach(enabled bool, run *outpostDial) (*bridge.Conn, protocol.OutpostReach) {
	if !enabled || run == nil || run.state == bridge.StateLost || run.state == bridge.StateRefused {
		return nil, protocol.OutpostReachUnreachable
	}
	if run.conn == nil {
		if run.state == bridge.StateConnecting {
			return nil, protocol.OutpostReachConnecting
		}
		return nil, protocol.OutpostReachUnreachable
	}
	select {
	case <-run.conn.Done():
		return nil, protocol.OutpostReachUnreachable
	default:
		return run.conn, protocol.OutpostReachReachable
	}
}

func (r *Router) Attach(ctx context.Context, t harness.TerminalID, subscriber string, opts ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	session, _ := r.daemon.shownIn(t)
	if host := r.daemon.sessionHost(session); host != "" {
		conn, _, err := r.daemon.outpostConn(host)
		if err != nil {
			return ptybackend.AttachInfo{}, nil, err
		}
		info, stream, err := conn.Attach(ctx, bridge.AttachRequest{Terminal: t, Subscriber: subscriber, Options: opts})
		return info, stream, terminalBridgeError(err)
	}
	return r.daemon.ptyBackend.Attach(ctx, t, subscriber, opts)
}

func runs[M protocol.Placed, R any](parts map[string]machinePart, readOnly bool, run func(context.Context, M) (R, error)) {
	var command M
	parts[command.CommandName()] = machinePart{
		readOnly: readOnly,
		run: func(ctx context.Context, msg protocol.Placed) (any, error) {
			typed, ok := msg.(M)
			if !ok {
				return nil, fmt.Errorf("%s carries %T, not %T", msg.CommandName(), msg, command)
			}
			return run(ctx, typed)
		},
		decode: func(raw []byte) (protocol.Placed, error) {
			var msg M
			err := json.Unmarshal(raw, &msg)
			return msg, err
		},
	}
}

func terminalBridgeError(err error) error {
	var rejected *bridge.CallRejected
	if errors.As(err, &rejected) && strings.Contains(rejected.Reason, pty.ErrSessionNotFound.Error()) {
		return fmt.Errorf("%w: %v", pty.ErrSessionNotFound, err)
	}
	return err
}
