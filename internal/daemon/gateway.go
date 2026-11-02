package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
)

// GatewayDevice is a device the caller has already authenticated, with the profile its pairing reaches.
type GatewayDevice struct {
	DeviceID  string
	ProfileID string
}

// GatewayStream carries one device's gateway messages, one JSON message per frame. Close must unblock Recv.
type GatewayStream interface {
	Recv() ([]byte, error)
	Send([]byte) error
	Close() error
}

const phoneAccessOff = "phone access is off on this home; turn it on in Settings → Experimental → Phone access"

var gatewayFacts = bus.Filter{
	FactSessionRegistered,
	FactSessionReregistered,
	FactSessionStateChanged,
	FactSessionTitleChanged,
	FactSessionRenamed,
	FactSessionUnregistered,
	FactSessionClosed,
	FactSessionTerminated,
	FactSessionReconciled,
	FactWorktreeSessionsRemoved,
	FactOutpostChanged,
	FactProfileRenamed,
	FactSettingChanged,
	FactAskOpened,
	FactAskClosed,
}

var gatewayStates = map[protocol.SessionState]gateway.GatewaySessionState{
	protocol.SessionStateWorking:         gateway.GatewaySessionStateWorking,
	protocol.SessionStateWaitingInput:    gateway.GatewaySessionStateWaiting,
	protocol.SessionStatePendingApproval: gateway.GatewaySessionStateNeedsApproval,
	protocol.SessionStateIdle:            gateway.GatewaySessionStateIdle,
	protocol.SessionStateScheduled:       gateway.GatewaySessionStateScheduled,
	protocol.SessionStateLaunching:       gateway.GatewaySessionStateLaunching,
	protocol.SessionStateRecoverable:     gateway.GatewaySessionStateRecoverable,
}

func gatewayState(state protocol.SessionState) gateway.GatewaySessionState {
	if mapped, ok := gatewayStates[state]; ok {
		return mapped
	}
	return gateway.GatewaySessionStateUnknown
}

type gatewayConns struct {
	mu    sync.Mutex
	conns map[*gatewayConn]struct{}
}

type gatewayConn struct {
	device  GatewayDevice
	stream  GatewayStream
	sendMu  sync.Mutex
	changed chan struct{}
	pinged  chan struct{}
	api     int
	sent    []gateway.GatewaySession

	sentAskIDs []string
	sentAsks   map[string][]byte

	order       terminalOrder
	terminalsMu sync.Mutex
	terminals   map[protocol.TerminalID]*gatewayTerminal
}

func (g *gatewayConns) add(conn *gatewayConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conns == nil {
		g.conns = map[*gatewayConn]struct{}{}
	}
	g.conns[conn] = struct{}{}
}

func (g *gatewayConns) remove(conn *gatewayConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conns, conn)
}

func (g *gatewayConns) nudge() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for conn := range g.conns {
		select {
		case conn.changed <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) phoneAccessEnabled() bool {
	return parseBooleanSetting(d.homeSetting(SettingPhoneAccessEnabled))
}

// ServeGateway answers one device until its stream ends, the home turns phone access off, or the daemon stops.
// A daemon stop closes the stream.
func (d *Daemon) ServeGateway(device GatewayDevice, stream GatewayStream) error {
	defer stream.Close()
	release, ok := d.life.Hold("gateway")
	if !ok {
		return errDaemonStopping
	}
	defer release()
	stopClosing := context.AfterFunc(d.life.Context(), func() { stream.Close() })
	defer stopClosing()

	conn := &gatewayConn{device: device, stream: stream, changed: make(chan struct{}, 1), pinged: make(chan struct{}, 1), terminals: map[protocol.TerminalID]*gatewayTerminal{}}
	d.gateway.add(conn)
	defer d.gateway.remove(conn)
	if err := d.gatewayWelcome(conn); err != nil {
		return err
	}

	ended := make(chan error, 1)
	received := make(chan struct{})
	if !d.life.Go("gatewayReceive", func() { defer close(received); ended <- d.gatewayReceive(conn) }) {
		return errDaemonStopping
	}
	defer func() {
		stream.Close()
		<-received
		d.gatewayDetachAll(conn)
	}()
	for {
		select {
		case <-conn.changed:
			if !d.phoneAccessEnabled() {
				return d.gatewayRefuse(conn, phoneAccessOff)
			}
			if err := d.gatewaySendState(conn); err != nil {
				return err
			}
		case <-conn.pinged:
			if err := gatewaySend(conn, gateway.GatewayPong{Event: "pong"}); err != nil {
				return err
			}
		case err := <-ended:
			return err
		}
	}
}

func (d *Daemon) gatewayWelcome(conn *gatewayConn) error {
	frame, err := conn.stream.Recv()
	if err != nil {
		return err
	}
	var hello gateway.GatewayHello
	if err := json.Unmarshal(frame, &hello); err != nil || hello.Cmd != "hello" {
		return d.gatewayRefuse(conn, "the first message must be hello")
	}
	if !d.phoneAccessEnabled() {
		return d.gatewayRefuse(conn, phoneAccessOff)
	}
	if hello.API < 1 {
		return d.gatewayRefuse(conn, fmt.Sprintf("hello asked for api %d; this home serves api 1 to %d", hello.API, gateway.APIVersion))
	}
	profile, err := d.store.GetProfile(conn.device.ProfileID)
	if err != nil || profile.Deleted() {
		return d.gatewayRefuse(conn, fmt.Sprintf("profile %s is gone from this home; pair the phone again", conn.device.ProfileID))
	}
	conn.api = min(hello.API, gateway.APIVersion)
	if err := gatewaySend(conn, gateway.GatewayWelcome{
		Event:    "welcome",
		API:      conn.api,
		HomeName: homeDisplayName(),
		Profile:  gateway.GatewayProfile{ID: profile.ID, Name: profile.Name},
	}); err != nil {
		return err
	}
	return d.gatewaySendState(conn)
}

func (d *Daemon) gatewaySendState(conn *gatewayConn) error {
	if err := d.gatewaySendSessions(conn); err != nil {
		return err
	}
	return d.gatewaySendAsks(conn)
}

func (d *Daemon) gatewayReceive(conn *gatewayConn) error {
	for {
		frame, err := conn.stream.Recv()
		if err != nil {
			return err
		}
		var message struct {
			Cmd string `json:"cmd"`
		}
		_ = json.Unmarshal(frame, &message)
		switch message.Cmd {
		case "ping":
			select {
			case conn.pinged <- struct{}{}:
			default:
			}
			continue
		case "ask_answer":
			var command gateway.GatewayAskAnswerCommand
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("device %s sent a malformed ask_answer: %w", conn.device.DeviceID, err)
			}
			if err := d.gatewayAnswerAsk(conn, command); err != nil {
				return err
			}
			continue
		case "ask_image":
			var command gateway.GatewayAskImage
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("device %s sent a malformed ask_image: %w", conn.device.DeviceID, err)
			}
			if err := d.gatewayAskImage(conn, command); err != nil {
				return err
			}
			continue
		}
		if conn.api >= gatewayTerminalAPI && d.gatewayCommand(conn, message.Cmd, frame) {
			continue
		}
		d.logf("gateway: device %s sent %q, which this home does not serve", conn.device.DeviceID, message.Cmd)
	}
}

func (d *Daemon) gatewayRefuse(conn *gatewayConn, message string) error {
	if err := gatewaySend(conn, gateway.GatewayRefused{Event: "refused", Message: message}); err != nil {
		return err
	}
	return errors.New("gateway refused device " + conn.device.DeviceID + ": " + message)
}

func (d *Daemon) gatewaySendSessions(conn *gatewayConn) error {
	sessions := d.gatewaySessions(conn.device.ProfileID)
	if conn.sent != nil && wireEqual(conn.sent, sessions) {
		return nil
	}
	if err := gatewaySend(conn, gateway.GatewaySessions{Event: "sessions", Sessions: sessions}); err != nil {
		return err
	}
	conn.sent = sessions
	return nil
}

func (d *Daemon) gatewaySessions(profileID string) []gateway.GatewaySession {
	sessions := []gateway.GatewaySession{}
	for _, session := range d.store.List("") {
		if session.ProfileID != profileID {
			continue
		}
		d.decorateSessionWithTitle(session)
		title := protocol.Deref(session.Title)
		if title == "" {
			title = session.Label
		}
		state := gatewayState(session.State)
		out := gateway.GatewaySession{ID: string(session.ID), Title: title, Agent: string(session.Agent), State: state}
		if terminal := d.primaryTerminal(session.ID); terminal != "" {
			out.TerminalID = protocol.Ptr(string(terminal))
		}
		if state == gateway.GatewaySessionStateWaiting || state == gateway.GatewaySessionStateNeedsApproval {
			if since := protocol.Timestamp(session.StateSince); !since.IsZero() {
				out.WaitingSince = protocol.Ptr(since.Time().UTC())
			}
		}
		if host := protocol.Deref(session.Host); host != "" {
			_, name, err := d.outpostConn(host)
			out.Host = protocol.Ptr(name)
			out.Offline = err != nil
		}
		sessions = append(sessions, out)
	}
	return sessions
}

func gatewaySend(conn *gatewayConn, message any) error {
	frame, err := gatewayMarshal(message)
	if err != nil {
		return err
	}
	return conn.sendRaw(frame)
}

func (c *gatewayConn) sendRaw(frame []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(frame)
}

func gatewayMarshal(message any) ([]byte, error) {
	var frame bytes.Buffer
	encoder := json.NewEncoder(&frame)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(frame.Bytes(), []byte("\n")), nil
}
