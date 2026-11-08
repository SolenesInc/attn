package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/setting"
)

// HomeGatewayPhone is a phone the caller has already authenticated, with the profile its pairing reaches.
type HomeGatewayPhone struct {
	PhoneID   string
	ProfileID string
}

// HomeGatewayStream carries one phone's Home Gateway messages, one JSON message per frame. Close must unblock Recv.
type HomeGatewayStream interface {
	Recv() ([]byte, error)
	Send([]byte) error
	Close() error
}

const homeGatewayCrewAPI = 8

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
	FactCrewRegistered,
	FactCrewBound,
	FactCrewReleased,
	FactCrewUpdated,
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
	phone         HomeGatewayPhone
	stream        HomeGatewayStream
	sendMu        sync.Mutex
	changed       chan struct{}
	gardenChanged chan struct{}
	pinged        chan struct{}
	api           int
	sent          *homegateway.HomeGatewaySessions

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

// ServeHomeGateway answers one phone until its stream ends, the home turns phone access off, or the daemon stops.
// A daemon stop closes the stream.
func (d *Daemon) ServeHomeGateway(phone HomeGatewayPhone, stream HomeGatewayStream) error {
	defer stream.Close()
	release, ok := d.life.Hold("home-gateway")
	if !ok {
		return errDaemonStopping
	}
	defer release()
	stopClosing := context.AfterFunc(d.life.Context(), func() { stream.Close() })
	defer stopClosing()

	conn := &homeGatewayConn{phone: phone, stream: stream, changed: make(chan struct{}, 1), gardenChanged: make(chan struct{}, 1), pinged: make(chan struct{}, 1), terminals: map[protocol.TerminalID]*homeGatewayTerminal{}}
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
	profile, err := d.store.GetProfile(conn.phone.ProfileID)
	if err != nil || profile.Deleted() {
		return d.homeGatewayRefuse(conn, fmt.Sprintf("profile %s is gone from this home; pair the phone again", conn.phone.ProfileID))
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
				return fmt.Errorf("phone %s sent a malformed ask_answer: %w", conn.phone.PhoneID, err)
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
		case "crew_wake":
			if conn.api < homeGatewayCrewAPI {
				break
			}
			var command homegateway.HomeGatewayCrewWake
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("phone %s sent a malformed crew_wake: %w", conn.phone.PhoneID, err)
			}
			d.life.Go("homeGatewayCrewWake", func() { d.homeGatewayCrewWake(conn, command) })
			continue
		case "ask_image":
			var command homegateway.HomeGatewayAskImage
			if err := json.Unmarshal(frame, &command); err != nil {
				return fmt.Errorf("phone %s sent a malformed ask_image: %w", conn.phone.PhoneID, err)
			}
			if err := d.homeGatewayAskImage(conn, command); err != nil {
				return err
			}
			continue
		}
		if conn.api >= homeGatewayTerminalAPI && d.homeGatewayCommand(conn, message.Cmd, frame) {
			continue
		}
		d.logf("Home Gateway: phone %s sent %q, which this home does not serve", conn.phone.PhoneID, message.Cmd)
	}
}

func (d *Daemon) homeGatewayRefuse(conn *homeGatewayConn, message string) error {
	if err := homeGatewaySend(conn, homegateway.HomeGatewayRefused{Event: "refused", Message: message}); err != nil {
		return err
	}
	return errors.New("the Home Gateway refused phone " + conn.phone.PhoneID + ": " + message)
}

func (d *Daemon) homeGatewaySendSessions(conn *homeGatewayConn) error {
	sessions := d.homeGatewaySessions(conn.phone.ProfileID)
	if conn.sent != nil && wireEqual(*conn.sent, sessions) {
		return nil
	}
	if err := homeGatewaySend(conn, sessions); err != nil {
		return err
	}
	conn.sent = &sessions
	return nil
}

func (d *Daemon) homeGatewaySessions(profileID string) homegateway.HomeGatewaySessions {
	roster := d.homeGatewayRoster(profileID)
	chief := d.chiefOfProfile(profileID)
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
		if member, bound := roster.bySession[session.ID]; bound {
			out.CrewMember, out.CrewMemberName = protocol.Ptr(member.ID), protocol.Ptr(member.Name)
		}
		if session.ID == chief {
			out.Chief = protocol.Ptr(true)
		}
		sessions = append(sessions, out)
	}
	return homegateway.HomeGatewaySessions{Event: "sessions", Sessions: sessions, AsleepCrew: roster.asleep}
}

type homeGatewayRoster struct {
	bySession map[protocol.SessionID]crew.Member
	asleep    []homegateway.HomeGatewayAsleepCrewMember
}

func (d *Daemon) homeGatewayRoster(profileID string) homeGatewayRoster {
	roster := homeGatewayRoster{bySession: map[protocol.SessionID]crew.Member{}}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("Home Gateway: reading the crew of profile %s: %v", profileID, err)
		}
		return roster
	}
	for _, member := range members {
		if member.ProfileID != profileID {
			continue
		}
		if d.crewBindingLive(member) {
			roster.bySession[member.BindingSession] = member
			continue
		}
		if member.Retired {
			continue
		}
		asleep := homegateway.HomeGatewayAsleepCrewMember{CrewMember: member.ID, CrewMemberName: member.Name}
		if member.Chief {
			asleep.Chief = protocol.Ptr(true)
		}
		roster.asleep = append(roster.asleep, asleep)
	}
	return roster
}

func (d *Daemon) homeGatewayCrewWake(conn *homeGatewayConn, command homegateway.HomeGatewayCrewWake) {
	result := homegateway.HomeGatewayCrewWakeResult{Event: "crew_wake_result", RequestID: command.RequestID, CrewMember: command.CrewMember}
	if _, err := d.crewWakeAsked(&protocol.CrewWakeMessage{Member: crew.KeyRefPrefix + command.CrewMember, ProfileID: protocol.Ptr(conn.phone.ProfileID)}, crewWakeRequest{RequestedBy: d.phoneName(conn.phone)}); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	_ = homeGatewaySend(conn, result)
}

func (d *Daemon) phoneName(phone HomeGatewayPhone) string {
	pairings, err := d.store.PhonePairings()
	if err != nil {
		d.logf("phones: reading the name of phone %s: %v", phone.PhoneID, err)
	}
	for _, pairing := range pairings {
		if pairing.PhoneID == phone.PhoneID && pairing.ProfileID == phone.ProfileID {
			return pairing.Name
		}
	}
	return "a phone"
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
