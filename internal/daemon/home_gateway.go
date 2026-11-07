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
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/setting"
)

// HomeGatewayDevice is a device the caller has already authenticated, with the profile its pairing reaches.
type HomeGatewayDevice struct {
	DeviceID  string
	ProfileID string
}

// HomeGatewayStream carries one device's Home Gateway messages, one JSON message per frame. Close must unblock Recv.
type HomeGatewayStream interface {
	Recv() ([]byte, error)
	Send([]byte) error
	Close() error
}

var phoneAccessOff = setting.PhoneAccessEnabled.Refusal().Error()

var homeGatewayFacts = bus.Filter{
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

var homeGatewayStates = map[protocol.SessionState]homegateway.HomeGatewaySessionState{
	protocol.SessionStateWorking:         homegateway.HomeGatewaySessionStateWorking,
	protocol.SessionStateWaitingInput:    homegateway.HomeGatewaySessionStateWaiting,
	protocol.SessionStatePendingApproval: homegateway.HomeGatewaySessionStateNeedsApproval,
	protocol.SessionStateIdle:            homegateway.HomeGatewaySessionStateIdle,
	protocol.SessionStateScheduled:       homegateway.HomeGatewaySessionStateScheduled,
	protocol.SessionStateLaunching:       homegateway.HomeGatewaySessionStateLaunching,
	protocol.SessionStateRecoverable:     homegateway.HomeGatewaySessionStateRecoverable,
}

func homeGatewayState(state protocol.SessionState) homegateway.HomeGatewaySessionState {
	if mapped, ok := homeGatewayStates[state]; ok {
		return mapped
	}
	return homegateway.HomeGatewaySessionStateUnknown
}

type homeGatewayConns struct {
	mu    sync.Mutex
	conns map[*homeGatewayConn]struct{}
}

type homeGatewayConn struct {
	device        HomeGatewayDevice
	stream        HomeGatewayStream
	sendMu        sync.Mutex
	changed       chan struct{}
	gardenChanged chan struct{}
	pinged        chan struct{}
	api           int
	sent          []homegateway.HomeGatewaySession

	sentAskIDs []string
	sentAsks   map[string][]byte

	sentSeedIDs []string
	sentSeeds   map[string][]byte

	changedSeedsMu sync.Mutex
	changedSeeds   map[string]struct{}

	order       terminalOrder
	terminalsMu sync.Mutex
	terminals   map[protocol.TerminalID]*homeGatewayTerminal
}

func (g *homeGatewayConns) add(conn *homeGatewayConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conns == nil {
		g.conns = map[*homeGatewayConn]struct{}{}
	}
	g.conns[conn] = struct{}{}
}

func (g *homeGatewayConns) remove(conn *homeGatewayConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conns, conn)
}

func (g *homeGatewayConns) nudge() {
	g.signal(func(conn *homeGatewayConn) chan struct{} { return conn.changed })
}

func (g *homeGatewayConns) nudgeGarden() {
	g.signal(func(conn *homeGatewayConn) chan struct{} { return conn.gardenChanged })
}

func (g *homeGatewayConns) seedChanged(seedID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for conn := range g.conns {
		conn.changedSeedsMu.Lock()
		if conn.changedSeeds == nil {
			conn.changedSeeds = map[string]struct{}{}
		}
		conn.changedSeeds[seedID] = struct{}{}
		conn.changedSeedsMu.Unlock()
		select {
		case conn.gardenChanged <- struct{}{}:
		default:
		}
	}
}

func (g *homeGatewayConns) signal(channel func(*homeGatewayConn) chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for conn := range g.conns {
		select {
		case channel(conn) <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) phoneAccessEnabled() bool {
	return d.on(setting.PhoneAccessEnabled)
}

// ServeHomeGateway answers one device until its stream ends, the home turns phone access off, or the daemon stops.
// A daemon stop closes the stream.
func (d *Daemon) ServeHomeGateway(device HomeGatewayDevice, stream HomeGatewayStream) error {
	defer stream.Close()
	release, ok := d.life.Hold("home-gateway")
	if !ok {
		return errDaemonStopping
	}
	defer release()
	stopClosing := context.AfterFunc(d.life.Context(), func() { stream.Close() })
	defer stopClosing()

	conn := &homeGatewayConn{device: device, stream: stream, changed: make(chan struct{}, 1), gardenChanged: make(chan struct{}, 1), pinged: make(chan struct{}, 1), terminals: map[protocol.TerminalID]*homeGatewayTerminal{}}
	d.homeGateway.add(conn)
	defer d.homeGateway.remove(conn)
	if err := d.homeGatewayWelcome(conn); err != nil {
		return err
	}

	ended := make(chan error, 1)
	received := make(chan struct{})
	if !d.life.Go("homeGatewayReceive", func() { defer close(received); ended <- d.homeGatewayReceive(conn) }) {
		return errDaemonStopping
	}
	defer func() {
		stream.Close()
		<-received
		d.homeGatewayDetachAll(conn)
	}()
	for {
		select {
		case <-conn.changed:
			if !d.phoneAccessEnabled() {
				return d.homeGatewayRefuse(conn, phoneAccessOff)
			}
			d.homeGatewayResumeAwaiting(conn)
			if err := d.homeGatewaySendState(conn); err != nil {
				return err
			}
		case <-conn.gardenChanged:
			if !d.phoneAccessEnabled() {
				return d.homeGatewayRefuse(conn, phoneAccessOff)
			}
			if err := d.homeGatewaySendGarden(conn); err != nil {
				return err
			}
			if err := d.homeGatewaySendSeedChanges(conn); err != nil {
				return err
			}
		case <-conn.pinged:
			if err := homeGatewaySend(conn, homegateway.HomeGatewayPong{Event: "pong"}); err != nil {
				return err
			}
		case err := <-ended:
			return err
		}
	}
}

func (d *Daemon) homeGatewayWelcome(conn *homeGatewayConn) error {
	frame, err := conn.stream.Recv()
	if err != nil {
		return err
	}
	var hello homegateway.HomeGatewayHello
	if err := json.Unmarshal(frame, &hello); err != nil || hello.Cmd != "hello" {
		return d.homeGatewayRefuse(conn, "the first message must be hello")
	}
	if !d.phoneAccessEnabled() {
		return d.homeGatewayRefuse(conn, phoneAccessOff)
	}
	select {
	case <-d.ptyRecovered:
	case <-d.life.Done():
		return errDaemonStopping
	}
	if hello.API < 1 {
		return d.homeGatewayRefuse(conn, fmt.Sprintf("hello asked for api %d; this home serves api 1 to %d", hello.API, homegateway.APIVersion))
	}
	profile, err := d.store.GetProfile(conn.device.ProfileID)
	if err != nil || profile.Deleted() {
		return d.homeGatewayRefuse(conn, fmt.Sprintf("profile %s is gone from this home; pair the phone again", conn.device.ProfileID))
	}
	conn.api = min(hello.API, homegateway.APIVersion)
	if err := homeGatewaySend(conn, homegateway.HomeGatewayWelcome{
		Event:    "welcome",
		API:      conn.api,
		HomeName: homeDisplayName(),
		Profile:  homegateway.HomeGatewayProfile{ID: profile.ID, Name: profile.Name},
	}); err != nil {
		return err
	}
	if err := d.homeGatewaySendState(conn); err != nil {
		return err
	}
	return d.homeGatewaySendGarden(conn)
}

func (d *Daemon) homeGatewaySendState(conn *homeGatewayConn) error {
	if err := d.homeGatewaySendSessions(conn); err != nil {
		return err
	}
	return d.homeGatewaySendAsks(conn)
}

func (d *Daemon) homeGatewayReceive(conn *homeGatewayConn) error {
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
			var command homegateway.HomeGatewayAskAnswerCommand
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("device %s sent a malformed ask_answer: %w", conn.device.DeviceID, err)
			}
			if err := d.homeGatewayAnswerAsk(conn, command); err != nil {
				return err
			}
			continue
		case "seed_get", "seed_note", "seed_move":
			if conn.api < homeGatewayGardenAPI {
				break
			}
			if err := d.homeGatewayGardenCommand(conn, message.Cmd, frame); err != nil {
				return err
			}
			continue
		case "ask_image":
			var command homegateway.HomeGatewayAskImage
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("device %s sent a malformed ask_image: %w", conn.device.DeviceID, err)
			}
			if err := d.homeGatewayAskImage(conn, command); err != nil {
				return err
			}
			continue
		}
		if conn.api >= homeGatewayTerminalAPI && d.homeGatewayCommand(conn, message.Cmd, frame) {
			continue
		}
		d.logf("Home Gateway: device %s sent %q, which this home does not serve", conn.device.DeviceID, message.Cmd)
	}
}

func (d *Daemon) homeGatewayRefuse(conn *homeGatewayConn, message string) error {
	if err := homeGatewaySend(conn, homegateway.HomeGatewayRefused{Event: "refused", Message: message}); err != nil {
		return err
	}
	return errors.New("the Home Gateway refused device " + conn.device.DeviceID + ": " + message)
}

func (d *Daemon) homeGatewaySendSessions(conn *homeGatewayConn) error {
	sessions := d.homeGatewaySessions(conn.device.ProfileID)
	if conn.sent != nil && wireEqual(conn.sent, sessions) {
		return nil
	}
	if err := homeGatewaySend(conn, homegateway.HomeGatewaySessions{Event: "sessions", Sessions: sessions}); err != nil {
		return err
	}
	conn.sent = sessions
	return nil
}

func (d *Daemon) homeGatewaySessions(profileID string) []homegateway.HomeGatewaySession {
	sessions := []homegateway.HomeGatewaySession{}
	for _, session := range d.store.List("") {
		if session.ProfileID != profileID {
			continue
		}
		state := homeGatewayState(session.State)
		out := homegateway.HomeGatewaySession{ID: string(session.ID), Title: d.phoneSessionTitle(session), Agent: string(session.Agent), State: state}
		if terminal := d.primaryTerminal(session.ID); terminal != "" {
			out.TerminalID = protocol.Ptr(string(terminal))
		}
		if state == homegateway.HomeGatewaySessionStateWaiting || state == homegateway.HomeGatewaySessionStateNeedsApproval {
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

func homeGatewaySend(conn *homeGatewayConn, message any) error {
	frame, err := homeGatewayMarshal(message)
	if err != nil {
		return err
	}
	return conn.sendRaw(frame)
}

func (c *homeGatewayConn) sendRaw(frame []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(frame)
}

func homeGatewayMarshal(message any) ([]byte, error) {
	var frame bytes.Buffer
	encoder := json.NewEncoder(&frame)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(frame.Bytes(), []byte("\n")), nil
}
