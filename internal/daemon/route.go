package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
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
	host := r.host(msg)
	if host != "" {
		conn, _, err := r.daemon.outpostConn(host)
		if err != nil {
			return answer, err
		}
		err = conn.Call(ctx, bridgeCallTerminal, msg, &answer)
		return answer, terminalBridgeError(err)
	}
	part, ok := r.here[msg.Command()]
	if !ok {
		return answer, fmt.Errorf("no machine part runs %s", msg.Command())
	}
	result, err := part.run(ctx, msg)
	if err != nil {
		return answer, err
	}
	answer, ok = result.(R)
	if !ok {
		return answer, fmt.Errorf("%s answers %T, not %T", msg.Command(), result, answer)
	}
	return answer, nil
}

func (r *Router) host(msg protocol.Placed) string {
	switch msg.Placement().Locator {
	case protocol.LocatorMachine:
		return msg.Locate()
	case protocol.LocatorSession:
		return r.daemon.sessionHost(protocol.SessionID(msg.Locate()))
	case protocol.LocatorTerminal:
		session := r.daemon.terminals().owner(harness.TerminalID(msg.Locate()))
		return r.daemon.sessionHost(session)
	}
	return ""
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
	if d.outpostsEnabled() {
		if run := r.dialers[row.ID]; run != nil && run.conn != nil && run.state != bridge.StateLost && run.state != bridge.StateRefused {
			select {
			case <-run.conn.Done():
			default:
				return run.conn, row.Name, nil
			}
		}
	}
	return nil, row.Name, fmt.Errorf("%s is unreachable; this needs it now", row.Name)
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
	parts[command.Command()] = machinePart{
		readOnly: readOnly,
		run: func(ctx context.Context, msg protocol.Placed) (any, error) {
			typed, ok := msg.(M)
			if !ok {
				return nil, fmt.Errorf("%s carries %T, not %T", msg.Command(), msg, command)
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
