package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

// maxHomeRedialDelay bounds how long a machine waits to redial its home. Timers stop while a laptop sleeps, so it
// is also the longest a woken machine waits before it redials. Chosen for that wait, not measured; a dial is one
// ssh exec.
const maxHomeRedialDelay = 30 * time.Second

// maxReverseHeader is a tripwire: a header holds a machine name.
const maxReverseHeader = 4 << 10

type reverseHeader struct {
	Join string `json:"join,omitempty"`
}

type reverseArrival struct {
	conn     *bridge.Conn
	seen     *bridge.Pin
	err      error
	recorded chan struct{}
}

func (a reverseArrival) close() {
	if a.conn != nil {
		_ = a.conn.Close()
	}
}

func (d *Daemon) startReverseBridgeSocket() error {
	listen := d.outpost.listen
	if listen == nil {
		listen = listenBridgeSocket
	}
	listener, err := listen("unix", filepath.Join(d.dataRoot, "reverse-bridge.sock"))
	if err != nil {
		return err
	}
	context.AfterFunc(d.life.Context(), func() { _ = listener.Close() })
	if !d.life.Go("reverseBridgeSocket", func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			if !d.life.Go("reverseBridge", func() { d.acceptReverseBridge(raw) }) {
				_ = raw.Close()
			}
		}
	}) {
		_ = listener.Close()
	}
	return nil
}

func (d *Daemon) acceptReverseBridge(raw net.Conn) {
	header, err := readReverseHeader(raw)
	if err != nil {
		_ = raw.Close()
		d.logf("reverse bridge: %v", err)
		return
	}
	var row store.Machine
	var joined bridge.Handlers
	known := false
	recorded := make(chan struct{})
	conn, seen, err := bridge.Dial(d.life.Context(), bridge.DialConfig{Conn: raw, Self: d.bridgeIdentity(), HandlersAfter: recorded, Recognize: func(hostKey string) (bridge.Recognized, *bridge.Refusal) {
		found, refusal := d.recognizeReverseMachine(hostKey, header.Join != "")
		switch {
		case refusal != nil:
			return bridge.Recognized{}, refusal
		case found != nil:
			row, known = *found, true
			return bridge.Recognized{Pin: &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}, MachineID: row.ID, Handlers: d.homeHandlers(row.ID)}, nil
		default:
			joined = bridge.Handlers{}
			return bridge.Recognized{Handlers: joined}, nil
		}
	}})
	arrival := reverseArrival{conn: conn, seen: seen, err: err, recorded: recorded}
	if peer, joining := joiningPeer(conn, err); !known && joined != nil && seen != nil && joining {
		id := cmp.Or(peer.MachineID, uuid.NewString())
		maps.Copy(joined, d.homeHandlers(id))
		row, err = d.addReverseMachine(id, header.Join, *seen)
		known = err == nil
	}
	if !known {
		arrival.close()
		d.logf("reverse bridge from %s refused: %v", header.Join, err)
		return
	}
	d.deliverReverseArrival(row.ID, arrival)
}

func joiningPeer(conn *bridge.Conn, err error) (bridge.Identity, bool) {
	var refusal *bridge.Refusal
	switch {
	case err == nil:
		return conn.Peer(), true
	case errors.As(err, &refusal) && refusal.Reason == "build" && refusal.Peer != nil:
		return *refusal.Peer, true
	}
	return bridge.Identity{}, false
}

func readReverseHeader(raw net.Conn) (reverseHeader, error) {
	_ = raw.SetReadDeadline(time.Now().Add(bridge.AnswerWithin))
	defer raw.SetReadDeadline(time.Time{})
	var line []byte
	var next [1]byte
	for next[0] != '\n' {
		if len(line) > maxReverseHeader {
			return reverseHeader{}, fmt.Errorf("reverse bridge header is longer than %d bytes", maxReverseHeader)
		}
		if _, err := io.ReadFull(raw, next[:]); err != nil {
			return reverseHeader{}, fmt.Errorf("read reverse bridge header: %w", err)
		}
		line = append(line, next[0])
	}
	var header reverseHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return reverseHeader{}, fmt.Errorf("parse reverse bridge header %q: %w", line, err)
	}
	return header, nil
}

func (d *Daemon) recognizeReverseMachine(hostKey string, joining bool) (*store.Machine, *bridge.Refusal) {
	home := d.bridgeIdentity().Name
	if !d.enrollmentStatus().IsHome() {
		return nil, &bridge.Refusal{Reason: "disabled", Detail: fmt.Sprintf("%s is an outpost of another home, so no machine can join it; join that home instead", home)}
	}
	if !d.on(setting.RemoteHomeEnabled) {
		return nil, &bridge.Refusal{Reason: "disabled", Detail: fmt.Sprintf("home %s does not take machines that dial it; run attn settings set remote_home_enabled true there", home)}
	}
	if !d.outpostsEnabled() {
		return nil, &bridge.Refusal{Reason: "disabled", Detail: fmt.Sprintf("home %s has outposts off; run attn settings set outposts_enabled true there", home)}
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return nil, &bridge.Refusal{Reason: "identity", Detail: fmt.Sprintf("home %s cannot read its machines: %v", home, err)}
	}
	for _, row := range rows {
		if row.HostKey != hostKey {
			continue
		}
		if !row.Reverse() {
			return nil, &bridge.Refusal{Reason: "duplicate", Detail: fmt.Sprintf("home %s already has this machine as outpost %s (%s), which it dials; remove that outpost before this machine joins", home, row.Name, row.Destination)}
		}
		return &row, nil
	}
	if !joining {
		return nil, &bridge.Refusal{Reason: "removed", Detail: fmt.Sprintf("home %s has no machine with this host key: it was removed there; run attn enrollment join to join again", home)}
	}
	return nil, nil
}

func (d *Daemon) addReverseMachine(id, name string, seen bridge.Pin) (store.Machine, error) {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	if conflict := d.outpostConflict(id, seen.HostKey, seen.DaemonID); conflict != nil {
		return store.Machine{}, conflict
	}
	if strings.TrimSpace(name) == "" {
		name = seen.DaemonID
	}
	row, err := d.store.AddReverseMachine(id, name, seen.HostKey, seen.DaemonID)
	if err != nil {
		return store.Machine{}, fmt.Errorf("add machine %s: %w", name, err)
	}
	d.startOutpostDial(row)
	d.configureNotebookSync()
	d.publishFact(FactOutpostChanged, row.ID, nil)
	return row, nil
}

func (r *outpostsRuntime) arrivalsLocked(id string) chan reverseArrival {
	if r.arrivals == nil {
		r.arrivals = map[string]chan reverseArrival{}
	}
	if r.arrivals[id] == nil {
		r.arrivals[id] = make(chan reverseArrival, 1)
	}
	return r.arrivals[id]
}

func (d *Daemon) deliverReverseArrival(id string, arrival reverseArrival) {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	rows, err := d.store.ListOutposts()
	if err != nil || !slices.ContainsFunc(rows, func(row store.Machine) bool { return row.ID == id }) || !d.outpostsEnabled() || !d.on(setting.RemoteHomeEnabled) {
		arrival.close()
		return
	}
	r.mu.Lock()
	arrivals := r.arrivalsLocked(id)
	var current *bridge.Conn
	if run := r.dialers[id]; run != nil {
		current = run.conn
	}
	r.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}
	for {
		select {
		case arrivals <- arrival:
			return
		default:
		}
		select {
		case stale := <-arrivals:
			stale.close()
		default:
		}
	}
}

func (d *Daemon) forgetReverseArrivals(id string) {
	r := &d.outposts
	r.mu.Lock()
	arrivals := r.arrivals[id]
	delete(r.arrivals, id)
	r.mu.Unlock()
	if arrivals == nil {
		return
	}
	select {
	case stale := <-arrivals:
		stale.close()
	default:
	}
}

func (d *Daemon) awaitReverseOutpost(ctx context.Context, row store.Machine, run *outpostDial) {
	r := &d.outposts
	r.mu.Lock()
	arrivals := r.arrivalsLocked(row.ID)
	r.mu.Unlock()
	pin := &bridge.Pin{HostKey: row.HostKey, DaemonID: row.DaemonID}
	for {
		select {
		case <-ctx.Done():
			return
		case arrival := <-arrivals:
			if _, _, stop := d.outpostDialed(ctx, &row, run, nil, pin, arrival.conn, arrival.seen, arrival.err, arrival.recorded); stop {
				return
			}
		}
	}
}

func refuseReverse(row store.Machine, action string) error {
	return protocol.Refuse(protocol.RefusalWrongState, "%s dials this home, so the home cannot %s it; do that on %s itself", row.Name, action, row.Name)
}

type homeDial struct {
	destination string
	joining     bool
	cancel      context.CancelFunc
	done        chan struct{}
	retry       chan struct{}
	outcome     chan error
}

func (h *homeDial) report(err error) {
	select {
	case h.outcome <- err:
	default:
	}
}

func (h *homeDial) kick() {
	select {
	case h.retry <- struct{}{}:
	default:
	}
}

func (d *Daemon) homeLostState() string {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dial != nil {
		return "dialing"
	}
	return "off"
}

func (d *Daemon) homeSSH(destination string) *outpostssh.Client {
	return outpostssh.For(destination, outpostssh.ControlPath(config.InstanceLabel(), d.daemonInstanceID), config.InstanceLabel())
}

func (d *Daemon) configureHomeDial() {
	status := d.enrollmentStatus()
	want := ""
	if status.IsOutpost() {
		want = status.Dial
	}
	r := &d.outpost
	r.mu.Lock()
	current, listening := r.dial, r.listener != nil
	r.mu.Unlock()
	if !listening || current != nil && current.destination == want {
		return
	}
	d.stopHomeDial(current)
	if want != "" {
		d.startHomeDial(want, false)
	}
}

func (d *Daemon) stopHomeDial(dial *homeDial) {
	if dial == nil {
		return
	}
	dial.cancel()
	<-dial.done
	r := &d.outpost
	r.mu.Lock()
	run, stopped := r.listener, r.dial == dial
	if stopped {
		r.dial = nil
	}
	r.mu.Unlock()
	if err := d.homeSSH(dial.destination).ExitMaster(d.life.Context()); err != nil {
		d.logf("home dial %s: stop ssh master: %v", dial.destination, err)
	}
	if stopped {
		d.setOutpostListenerState(run, "off", nil)
	}
}

func (d *Daemon) startHomeDial(destination string, joining bool) *homeDial {
	ctx, cancel := context.WithCancel(d.life.Context())
	dial := &homeDial{destination: destination, joining: joining, cancel: cancel, done: make(chan struct{}), retry: make(chan struct{}, 1), outcome: make(chan error, 1)}
	r := &d.outpost
	r.mu.Lock()
	r.dial = dial
	run := r.listener
	r.mu.Unlock()
	if !d.life.Go("homeDial", func() { defer close(dial.done); d.runHomeDial(ctx, dial, run) }) {
		cancel()
		close(dial.done)
	}
	return dial
}

func (d *Daemon) runHomeDial(ctx context.Context, dial *homeDial, run *outpostListener) {
	retryDelay := bridge.ReconnectInterval
	d.setOutpostListenerState(run, "dialing", nil)
	for ctx.Err() == nil {
		err := d.dialHomeOnce(ctx, dial, run)
		if ctx.Err() != nil {
			dial.report(ctx.Err())
			return
		}
		dial.report(err)
		var refusal *bridge.Refusal
		var sshError *outpostssh.Error
		if errors.As(err, &refusal) && refusal.Reason == "removed" && d.leaveRemovedHome(refusal) {
			return
		}
		state, delay := "dialing", retryDelay
		switch {
		case err == nil:
			delay, retryDelay = bridge.ReconnectInterval, bridge.ReconnectInterval
		case refusal != nil, errors.As(err, &sshError) && sshError.Connectable():
			state, delay = "refused", max(retryDelay, bridge.RefusedRetryInterval)
		}
		if err != nil {
			retryDelay = min(delay*2, maxHomeRedialDelay)
			d.logf("home dial %s %s: %v; redialing in %s", dial.destination, state, err, delay)
		}
		d.setOutpostListenerState(run, state, err)
		woke, ok := waitHomeRedial(ctx, dial, delay)
		if !ok {
			return
		}
		if woke {
			retryDelay = bridge.ReconnectInterval
		}
	}
}

func (d *Daemon) dialHomeOnce(ctx context.Context, dial *homeDial, run *outpostListener) error {
	client := d.homeSSH(dial.destination)
	if err := client.EnsureMaster(ctx); err != nil {
		return err
	}
	pipe, err := client.ReverseBridge(ctx)
	if err != nil {
		return err
	}
	header := reverseHeader{}
	if dial.joining {
		header.Join = d.bridgeIdentity().Name
	}
	line, err := json.Marshal(header)
	if err == nil {
		_, err = pipe.Write(append(line, '\n'))
	}
	if err != nil {
		_ = pipe.Close()
		return err
	}
	return bridge.ServeConn(ctx, pipe, d.outpostServerConfig(dial.destination), func(conn *bridge.Conn) {
		dial.joining = false
		dial.report(nil)
		d.acceptOutpostBridge(run, conn)
	})
}

func waitHomeRedial(ctx context.Context, dial *homeDial, delay time.Duration) (woke, ok bool) {
	started := time.Now()
	timer := stepclock.NewTimer(bridge.RetryTimer, delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, false
	case <-dial.retry:
		return true, true
	case <-timer.C():
		slept := time.Now().Round(0).Sub(started.Round(0)) - time.Since(started)
		return slept > delay, true
	}
}

func (d *Daemon) leaveRemovedHome(refusal *bridge.Refusal) bool {
	status := d.enrollmentStatus()
	if refusal.Peer == nil || !status.IsOutpost() || refusal.Peer.HomeID != status.HomeID {
		return false
	}
	if _, err := enrollment.Leave(d.dataRoot, status.HomeID); err != nil {
		d.logf("home %s removed this machine, but leaving it failed: %v", status.HomeID, err)
		return false
	}
	d.logf("home %s removed this machine; it is its own home again", status.HomeID)
	d.life.Go("leaveRemovedHome", func() {
		if err := d.dropStaleCopy(); err != nil {
			d.logf("drop the copy of home %s: %v", status.HomeID, err)
		}
	})
	return true
}

func (d *Daemon) openSessionNames() []string {
	var names []string
	for _, session := range d.store.List("") {
		if d.copiedSession(session.ID) {
			continue
		}
		name := string(session.ID)
		if session.Label != "" {
			name = fmt.Sprintf("%s (%s)", session.Label, session.ID)
		}
		names = append(names, name)
	}
	return names
}

func (d *Daemon) ownSessionsRefusal(home bridge.Identity) *bridge.Refusal {
	if !d.enrollmentStatus().IsHome() {
		return nil
	}
	names := d.openSessionNames()
	if len(names) == 0 {
		return nil
	}
	return &bridge.Refusal{Reason: "sessions", Detail: fmt.Sprintf("this machine is its own home and has %d open session(s): %s; close them before it joins home %s", len(names), strings.Join(names, ", "), home.Name)}
}

func (d *Daemon) joinHome(ctx context.Context, destination string) (protocol.HomeDial, error) {
	if err := validateOutpostDestination(destination); err != nil {
		return protocol.HomeDial{}, err
	}
	status := d.enrollmentStatus()
	r := &d.outpost
	r.mu.Lock()
	current := r.dial
	r.mu.Unlock()
	switch {
	case status.IsOutpost() && status.Dial == destination && current != nil:
		current.kick()
		return d.homeDialState(), nil
	case status.IsOutpost():
		return protocol.HomeDial{}, protocol.Refuse(protocol.RefusalWrongState, "this machine is enrolled to home %s; run {attn enrollment leave} before it joins another", status.HomeID)
	case !d.on(setting.RemoteHomeEnabled):
		return protocol.HomeDial{}, protocol.Refuse(protocol.RefusalWrongState, "remote_home_enabled is off; run {attn settings set remote_home_enabled true} before you join a home")
	}
	if names := d.openSessionNames(); len(names) > 0 {
		return protocol.HomeDial{}, protocol.Refuse(protocol.RefusalWrongState, "this machine is its own home and has %d open session(s): %s; close them before it joins a home", len(names), strings.Join(names, ", "))
	}
	d.stopHomeDial(current)
	dial := d.startHomeDial(destination, true)
	var err error
	select {
	case err = <-dial.outcome:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil && d.enrollmentStatus().Dial != destination {
		d.stopHomeDial(dial)
	}
	state := d.homeDialState()
	if err == nil {
		state.State = "bridged"
	}
	return state, err
}

func (d *Daemon) homeDialState() protocol.HomeDial {
	state, _, lastError := d.outpostSettings()
	result := protocol.HomeDial{Destination: d.enrollmentStatus().Dial, State: state}
	if lastError != "" {
		result.LastError = protocol.Ptr(lastError)
	}
	return result
}

func (d *Daemon) handleJoinHome(ctx context.Context, conn net.Conn, msg *protocol.JoinHomeMessage) {
	dial, err := d.joinHome(ctx, msg.Destination)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, HomeDial: &dial})
}

func (d *Daemon) handleHomeDialStatus(conn net.Conn) {
	dial := d.homeDialState()
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, HomeDial: &dial})
}
