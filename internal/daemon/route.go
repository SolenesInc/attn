package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
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

func (r *Router) Send(msg protocol.Placed, answered func(error)) {
	release, ok := r.daemon.life.Hold("terminalSend")
	if !ok {
		answered(errDaemonStopping)
		return
	}
	answer := func(err error) {
		defer release()
		answered(err)
	}
	conn, err := r.terminalBridge(protocol.TerminalID(msg.Locate()))
	if err != nil {
		answer(err)
		return
	}
	if conn != nil {
		conn.Send(msg, func(err error) { answer(terminalBridgeError(err)) })
		pausepoint.At(pausepoint.BridgeSendQueued)
		return
	}
	part, ok := r.here[msg.CommandName()]
	if !ok {
		answer(fmt.Errorf("no machine part runs %s", msg.CommandName()))
		return
	}
	_, err = part.run(context.Background(), msg)
	answer(err)
}

type Origin struct {
	Host, Name    string
	Caller        protocol.SessionID
	CallerProfile string
	bridge        *bridge.Conn
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
	case protocol.LocatorHome:
		home = true
	case protocol.LocatorSetting:
		spec, err := d.writableSettingHere(msg.Locate())
		if err != nil {
			return nil, err
		}
		home = spec.Class == setting.Home
	case protocol.LocatorMachine:
		host = msg.Locate()
	case protocol.LocatorSession:
		place := d.sessionPlace(protocol.SessionID(msg.Locate()))
		host, home = place.Outpost(), !place.Known()
	case protocol.LocatorTerminal:
		place := d.sessionPlace(d.terminals().owner(harness.TerminalID(msg.Locate())))
		host, home = place.Outpost(), !place.Known()
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

func (d *Daemon) homeUnreachable(name string) error {
	if notice := d.homeUpdateNoticeText(name); notice != "" {
		return &homeUnreachableError{home: name, message: notice + "; this call did not run"}
	}
	return &homeUnreachableError{home: name, message: name + " is unreachable; this needs it now"}
}

func (d *Daemon) homeConn(status enrollment.Status) (*bridge.Conn, string, error) {
	name := status.HomeName
	if name == "" {
		name = status.HomeDaemonID
	}
	if conn := d.homeBridge(); conn != nil {
		return conn, name, nil
	}
	return nil, name, d.homeUnreachable(name)
}

type homeUnreachableError struct{ home, message string }

func (e *homeUnreachableError) Error() string { return e.message }

type homeLostError struct{ home, message string }

func (e *homeLostError) Error() string { return e.message }

func homeForwardRefusal(err error) *command.Refusal {
	var unreachable *homeUnreachableError
	if errors.As(err, &unreachable) {
		return &command.Refusal{Code: "home_unreachable", Message: err.Error(), Fix: "Nothing ran. Make the same call again once " + unreachable.home + " is reachable."}
	}
	var lost *homeLostError
	if errors.As(err, &lost) {
		return &command.Refusal{Code: "home_lost", Message: err.Error(), Fix: "It may have run. Check its effect at " + lost.home + " before making the call again."}
	}
	return command.AsRefusal(err)
}

func (d *Daemon) forwardCommand(ctx context.Context, c command.Caller, name string, args json.RawMessage) (any, *command.Refusal) {
	msg := protocol.RunCommandMessage{Cmd: protocol.CmdRunCommand, Command: name, Args: args}
	if c.Session != "" {
		msg.SessionID = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		msg.ProfileID = protocol.Ptr(c.Profile)
	}
	if c.Terminal != "" {
		msg.CallerID = protocol.Ptr(c.Terminal)
	}
	if c.Directory != "" {
		msg.Directory = protocol.Ptr(c.Directory)
	}
	msg.GitEnvironment = c.GitEnvironment
	frame, err := json.Marshal(msg)
	if err != nil {
		return nil, command.AsRefusal(err)
	}
	var out bytes.Buffer
	if err := d.router().Forward(ctx, append(frame, '\n'), forwardRequest{Caller: c.Session}, nil, &out); err != nil {
		return nil, homeForwardRefusal(err)
	}
	var reply protocol.Response
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(nil, protocol.MaxSocketFrameBytes)
	for scanner.Scan() {
		reply = protocol.Response{}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			return nil, command.AsRefusal(fmt.Errorf("the home's reply to %s: %w", name, err))
		}
	}
	switch {
	case reply.Ok && protocol.Deref(reply.CommandShownOnce):
		return command.Then{Result: reply.CommandResult, ShownOnce: true}, nil
	case reply.Ok:
		return reply.CommandResult, nil
	case reply.CommandRefusal != nil:
		return nil, &command.Refusal{Code: reply.CommandRefusal.Code, Message: reply.CommandRefusal.Message, Fix: protocol.Deref(reply.CommandRefusal.Fix)}
	default:
		return nil, &command.Refusal{Code: string(protocol.RefusalUnclassified), Message: protocol.Deref(reply.Error)}
	}
}

func (d *Daemon) sessionHost(id protocol.SessionID) string {
	return d.sessionPlace(id).Outpost()
}

func (d *Daemon) localDirectory(session *protocol.Session) string {
	if session == nil {
		return ""
	}
	dir, ok := d.sessionPlace(session.ID).local(session.Directory)
	if !ok {
		return ""
	}
	return dir
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

func (r *Router) terminalBridge(t harness.TerminalID) (*bridge.Conn, error) {
	host := r.daemon.sessionHost(r.daemon.terminals().owner(t))
	if host == "" {
		return nil, nil
	}
	conn, _, err := r.daemon.outpostConn(host)
	return conn, err
}

func (r *Router) Attach(ctx context.Context, t harness.TerminalID, subscriber string, opts ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	conn, err := r.terminalBridge(t)
	if err != nil {
		return ptybackend.AttachInfo{}, nil, err
	}
	if conn != nil {
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
