package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

const homeGatewayTerminalAPI = 4

// homeGatewayOutputCoalesce batches PTY output so a streaming terminal sends about 60 frames a second, inside the
// Cloud Relay's measured 4,000 messages a second for a whole home (plan F2).
const homeGatewayOutputCoalesce = 16 * time.Millisecond

// homeGatewayPendingTripwire bounds output held for a phone that stopped granting credit. Past it, a fresh screen
// replaces the backlog: a phone that far behind gains nothing from replaying it.
const homeGatewayPendingTripwire = 1 << 20

type homeGatewayTerminal struct {
	id   protocol.TerminalID
	conn *homeGatewayConn

	mu         sync.Mutex
	stream     ptybackend.Stream
	pending    []homeGatewayChunk
	pendingLen int
	floor      uint32
	credit     int
	// A screen covers the output dropped before it was asked for; one asked for during a snapshot needs another.
	screensAsked   uint64
	screensCovered uint64
	flushing       bool
	detached       bool
	awaitingHost   bool
}

type homeGatewayChunk struct {
	output []byte
	seq    uint32
	resize *homegateway.TerminalResized
	ended  bool
}

func (c *homeGatewayConn) commandOrder() *terminalOrder { return &c.order }

func (d *Daemon) homeGatewayCommand(conn *homeGatewayConn, cmd string, frame []byte) bool {
	switch cmd {
	case "terminal_attach":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalAttach](d, conn, frame); ok {
			d.homeGatewayAttach(conn, message)
		}
	case "terminal_credit":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalCredit](d, conn, frame); ok {
			if terminal := conn.terminal(protocol.TerminalID(message.TerminalID)); terminal != nil {
				d.homeGatewayGrant(terminal, message.Bytes)
			}
		}
	case "terminal_detach":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalDetach](d, conn, frame); ok {
			d.homeGatewayDetach(conn, protocol.TerminalID(message.TerminalID))
		}
	case "terminal_input":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalInput](d, conn, frame); ok && d.homeGatewayAttached(conn, message.TerminalID) {
			d.homeGatewayInput(conn, protocol.TerminalID(message.TerminalID), message.Data)
		}
	case "terminal_resize":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalResize](d, conn, frame); ok && d.homeGatewayAttached(conn, message.TerminalID) {
			d.homeGatewayRequestSize(conn, protocol.TerminalID(message.TerminalID), message.Cols, message.Rows)
		}
	case "terminal_focus":
		if message, ok := decodeHomeGatewayCommand[homegateway.TerminalFocus](d, conn, frame); ok && d.homeGatewayAttached(conn, message.TerminalID) {
			d.claimPtySize(conn, protocol.TerminalID(message.TerminalID))
			d.noteTerminalFocus(protocol.TerminalID(message.TerminalID))
		}
	case "submit_prompt":
		if message, ok := decodeHomeGatewayCommand[homegateway.SubmitPrompt](d, conn, frame); ok {
			d.life.Go("homeGatewaySubmitPrompt", func() { d.homeGatewaySubmitPrompt(conn, message) })
		}
	default:
		return false
	}
	return true
}

func decodeHomeGatewayCommand[T any](d *Daemon, conn *homeGatewayConn, frame []byte) (T, bool) {
	var message T
	if err := json.Unmarshal(frame, &message); err != nil {
		d.logf("Home Gateway: phone %s sent %s, which does not decode: %v", conn.phone.PhoneID, frame, err)
		return message, false
	}
	return message, true
}

func (d *Daemon) homeGatewayAttached(conn *homeGatewayConn, id string) bool {
	if conn.terminal(protocol.TerminalID(id)) != nil {
		return true
	}
	d.homeGatewayTerminalError(conn, protocol.TerminalID(id), fmt.Sprintf("terminal %s is not open on this phone; send terminal_attach first", id))
	return false
}

func (c *homeGatewayConn) terminal(id protocol.TerminalID) *homeGatewayTerminal {
	c.terminalsMu.Lock()
	defer c.terminalsMu.Unlock()
	return c.terminals[id]
}

func (d *Daemon) homeGatewayTerminalOfProfile(conn *homeGatewayConn, terminal protocol.TerminalID) bool {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && session.ProfileID == conn.phone.ProfileID
}

func (d *Daemon) homeGatewayAttach(conn *homeGatewayConn, message homegateway.TerminalAttach) {
	id := protocol.TerminalID(message.TerminalID)
	if !d.homeGatewayTerminalOfProfile(conn, id) {
		d.homeGatewayTerminalError(conn, id, fmt.Sprintf("terminal %s is not a session terminal of this pairing's profile", id))
		return
	}
	d.homeGatewayDetach(conn, id)
	if !d.homeGatewayRequestSize(conn, id, message.Cols, message.Rows) {
		return
	}
	terminal := &homeGatewayTerminal{id: id, conn: conn, credit: message.Credit, screensAsked: 1}
	stream, err := d.homeGatewayOpenStream(terminal)
	if err != nil {
		d.homeGatewayTerminalError(conn, id, "could not open the terminal: "+err.Error())
		d.homeGatewayReleaseSize(conn, id)
		return
	}
	terminal.stream = stream
	conn.terminalsMu.Lock()
	conn.terminals[id] = terminal
	conn.terminalsMu.Unlock()
	if !d.life.Go("homeGatewayTerminal", func() { d.runHomeGatewayTerminal(terminal, stream) }) {
		d.homeGatewayDetach(conn, id)
		return
	}
	d.homeGatewayFlushSoon(terminal, 0)
}

func (d *Daemon) homeGatewayOpenStream(terminal *homeGatewayTerminal) (ptybackend.Stream, error) {
	subscriber := fmt.Sprintf("home-gateway:%s:%p", terminal.conn.phone.PhoneID, terminal)
	_, stream, err := d.router().Attach(context.Background(), terminal.id, subscriber, ptybackend.AttachOptions{OmitReplay: true})
	return stream, err
}

func (d *Daemon) homeGatewayDetach(conn *homeGatewayConn, id protocol.TerminalID) {
	conn.terminalsMu.Lock()
	terminal := conn.terminals[id]
	delete(conn.terminals, id)
	conn.terminalsMu.Unlock()
	if terminal != nil {
		terminal.detach()
	}
	d.homeGatewayReleaseSize(conn, id)
}

func (d *Daemon) homeGatewayEnd(terminal *homeGatewayTerminal) {
	conn := terminal.conn
	conn.terminalsMu.Lock()
	current := conn.terminals[terminal.id] == terminal
	if current {
		delete(conn.terminals, terminal.id)
	}
	conn.terminalsMu.Unlock()
	terminal.detach()
	if current {
		d.homeGatewayReleaseSize(conn, terminal.id)
	}
}

func (d *Daemon) homeGatewayDetachAll(conn *homeGatewayConn) {
	conn.terminalsMu.Lock()
	terminals := conn.terminals
	conn.terminals = map[protocol.TerminalID]*homeGatewayTerminal{}
	conn.terminalsMu.Unlock()
	for _, terminal := range terminals {
		terminal.detach()
	}
	d.releasePtySizes(conn)
}

func (t *homeGatewayTerminal) detach() {
	t.mu.Lock()
	t.detached = true
	stream := t.stream
	t.pending = nil
	t.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func (d *Daemon) homeGatewayReleaseSize(conn *homeGatewayConn, id protocol.TerminalID) {
	if next, handedOn := d.ptySizes.release(conn, id); handedOn {
		d.applyPtySize(next.owner, id)
	}
}

func (d *Daemon) homeGatewayRequestSize(conn *homeGatewayConn, id protocol.TerminalID, cols, rows int) bool {
	if cols <= 0 || rows <= 0 || cols > maxPTYDimValue || rows > maxPTYDimValue {
		d.homeGatewayTerminalError(conn, id, fmt.Sprintf("invalid terminal size cols=%d rows=%d (expected 1..%d)", cols, rows, maxPTYDimValue))
		return false
	}
	if d.ptySizes.request(conn, protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: id, Cols: cols, Rows: rows}) {
		d.applyPtySize(conn, id)
	}
	return true
}

func (d *Daemon) homeGatewayInput(conn *homeGatewayConn, id protocol.TerminalID, data string) {
	d.claimPtySize(conn, id)
	d.sendInTerminalOrder(conn, id, func(answered func()) {
		write := protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: id, Data: data}
		d.router().Send(write, func(err error) {
			answered()
			if err != nil {
				d.homeGatewayTerminalError(conn, id, "the terminal refused input: "+err.Error())
			}
		})
	})
}

func (d *Daemon) homeGatewaySubmitPrompt(conn *homeGatewayConn, message homegateway.SubmitPrompt) {
	result := homegateway.SubmitResult{Event: "submit_result", RequestID: message.RequestID}
	sessionID := protocol.SessionID(message.SessionID)
	session := d.store.Get(sessionID)
	switch {
	case session == nil || session.ProfileID != conn.phone.ProfileID:
		result.Message = protocol.Ptr(fmt.Sprintf("session %s is not a session of this pairing's profile", sessionID))
	case strings.TrimSpace(message.Text) == "":
		result.Message = protocol.Ptr("there is no text to send")
	default:
		attempt := d.placeDelivery(context.Background(), sessionID, sessionmonitor.ComposedInput(message.RequestID, message.Text))
		if attempt.Stage == sessionmonitor.Placed {
			result.Delivered = true
		} else {
			result.Message = protocol.Ptr(attempt.Err.Error())
		}
	}
	_ = homeGatewaySend(conn, result)
}

func (d *Daemon) homeGatewayTerminalError(conn *homeGatewayConn, id protocol.TerminalID, message string) {
	d.logf("Home Gateway: phone %s, terminal %s: %s", conn.phone.PhoneID, id, message)
	_ = homeGatewaySend(conn, homegateway.TerminalError{Event: "terminal_error", TerminalID: string(id), Message: message})
}

func (d *Daemon) runHomeGatewayTerminal(terminal *homeGatewayTerminal, stream ptybackend.Stream) {
	for {
		d.homeGatewayReadStream(terminal, stream)
		if terminal.isDetached() {
			return
		}
		if d.homeGatewayTerminalGone(terminal.id) {
			d.homeGatewayTerminalEnded(terminal)
			return
		}
		if host := d.sessionHost(d.terminals().owner(terminal.id)); host != "" {
			next, err := d.homeGatewayOpenStream(terminal)
			if err != nil {
				d.homeGatewayAwaitHost(terminal, host, err)
				return
			}
			if !terminal.reopened(next) {
				_ = next.Close()
				return
			}
			stream = next
			d.homeGatewayFlushSoon(terminal, 0)
			continue
		}
		if !d.terminalNeedsResync(d.life.Context(), terminal.id) {
			d.homeGatewayTerminalEnded(terminal)
			return
		}
		next, err := d.homeGatewayOpenStream(terminal)
		if err != nil {
			d.homeGatewayTerminalError(terminal.conn, terminal.id, "lost the terminal and could not reopen it: "+err.Error())
			return
		}
		if !terminal.reopened(next) {
			_ = next.Close()
			return
		}
		stream = next
		d.homeGatewayFlushSoon(terminal, 0)
	}
}

func (d *Daemon) homeGatewayAwaitHost(terminal *homeGatewayTerminal, host string, err error) {
	terminal.mu.Lock()
	terminal.stream = nil
	terminal.awaitingHost = !terminal.detached
	terminal.mu.Unlock()
	_, name, _ := d.outpostConn(host)
	d.homeGatewayTerminalError(terminal.conn, terminal.id, fmt.Sprintf("lost the terminal on %s (%v); it comes back when %s answers again", name, err, name))
}

// homeGatewayResumeAwaiting runs on every change the Home Gateway hears, so a terminal whose outpost answers again resumes.
func (d *Daemon) homeGatewayResumeAwaiting(conn *homeGatewayConn) {
	conn.terminalsMu.Lock()
	var resuming []*homeGatewayTerminal
	for _, terminal := range conn.terminals {
		if terminal.takeAwaiting() {
			resuming = append(resuming, terminal)
		}
	}
	conn.terminalsMu.Unlock()
	for _, terminal := range resuming {
		d.life.Go("homeGatewayResumeTerminal", func() {
			if d.homeGatewayTerminalGone(terminal.id) {
				d.homeGatewayTerminalEnded(terminal)
				return
			}
			stream, err := d.homeGatewayOpenStream(terminal)
			if err != nil {
				terminal.backToAwaiting()
				return
			}
			if !terminal.reopened(stream) {
				_ = stream.Close()
				return
			}
			d.homeGatewayFlushSoon(terminal, 0)
			d.runHomeGatewayTerminal(terminal, stream)
		})
	}
}

func (d *Daemon) homeGatewayTerminalGone(id protocol.TerminalID) bool {
	session, shown := d.shownIn(id)
	return !shown || d.store.GetSessionExit(session) != nil
}

func (d *Daemon) homeGatewayTerminalEnded(terminal *homeGatewayTerminal) {
	terminal.queue(homeGatewayChunk{ended: true})
	d.homeGatewayFlushSoon(terminal, 0)
}

func (t *homeGatewayTerminal) takeAwaiting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	taken := t.awaitingHost
	t.awaitingHost = false
	return taken
}

func (t *homeGatewayTerminal) backToAwaiting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.awaitingHost = !t.detached
}

func (d *Daemon) homeGatewayReadStream(terminal *homeGatewayTerminal, stream ptybackend.Stream) {
	defer stream.Close()
	for event := range stream.Events() {
		desync := event.Kind == ptybackend.OutputEventKindDesync
		switch event.Kind {
		case ptybackend.OutputEventKindOutput:
			terminal.output(event.Seq, event.Data)
		case ptybackend.OutputEventKindResize:
			terminal.queue(homeGatewayChunk{resize: &homegateway.TerminalResized{Event: "terminal_resized", TerminalID: string(terminal.id), Cols: int(event.Cols), Rows: int(event.Rows)}})
		}
		if acknowledged, ok := stream.(interface{ Acknowledge() }); ok {
			acknowledged.Acknowledge()
		}
		if desync {
			return
		}
		d.homeGatewayFlushSoon(terminal, homeGatewayOutputCoalesce)
	}
}

func (t *homeGatewayTerminal) isDetached() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.detached
}

func (t *homeGatewayTerminal) reopened(stream ptybackend.Stream) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detached {
		return false
	}
	t.stream = stream
	t.awaitingHost = false
	t.pending = nil
	t.pendingLen = 0
	t.screensAsked++
	return true
}

func (t *homeGatewayTerminal) output(seq uint32, data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detached || (!t.needsScreenLocked() && seq <= t.floor) {
		return
	}
	if t.pendingLen+len(data) > homeGatewayPendingTripwire {
		t.pending = nil
		t.pendingLen = 0
		t.screensAsked++
		return
	}
	t.pending = append(t.pending, homeGatewayChunk{output: data, seq: seq})
	t.pendingLen += len(data)
}

func (t *homeGatewayTerminal) needsScreenLocked() bool {
	return t.screensAsked > t.screensCovered
}

func (t *homeGatewayTerminal) queue(chunk homeGatewayChunk) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.detached {
		t.pending = append(t.pending, chunk)
	}
}

func (d *Daemon) homeGatewayGrant(terminal *homeGatewayTerminal, bytes int) {
	terminal.mu.Lock()
	terminal.credit += bytes
	terminal.mu.Unlock()
	d.homeGatewayFlushSoon(terminal, 0)
}

func (d *Daemon) homeGatewayFlushSoon(terminal *homeGatewayTerminal, delay time.Duration) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.flushing || !terminal.hasWorkLocked() {
		return
	}
	terminal.flushing = true
	d.life.AfterFunc("homeGatewayFlush", delay, func() { d.homeGatewayFlush(terminal) })
}

func (t *homeGatewayTerminal) hasWorkLocked() bool {
	if t.detached {
		return false
	}
	if t.needsScreenLocked() {
		return t.credit >= cloudlink.MaxMessageBytes
	}
	return t.credit > 0 && len(t.pending) > 0
}

func (d *Daemon) homeGatewayFlush(terminal *homeGatewayTerminal) {
	for {
		terminal.mu.Lock()
		if !terminal.hasWorkLocked() {
			terminal.flushing = false
			terminal.mu.Unlock()
			return
		}
		if terminal.needsScreenLocked() {
			terminal.mu.Unlock()
			if !d.homeGatewaySendScreen(terminal) {
				return
			}
			continue
		}
		message, ended := terminal.nextMessageLocked()
		terminal.mu.Unlock()
		if err := terminal.send(message); err != nil {
			terminal.stopFlushing()
			return
		}
		if ended {
			d.homeGatewayEnd(terminal)
			return
		}
	}
}

func (t *homeGatewayTerminal) stopFlushing() {
	t.mu.Lock()
	t.flushing = false
	t.mu.Unlock()
}

func (t *homeGatewayTerminal) nextMessageLocked() ([]byte, bool) {
	head := t.pending[0]
	switch {
	case head.ended:
		t.pending = t.pending[1:]
		frame, _ := json.Marshal(homegateway.TerminalEnded{Event: "terminal_ended", TerminalID: string(t.id)})
		return frame, true
	case head.resize != nil:
		t.pending = t.pending[1:]
		frame, _ := json.Marshal(head.resize)
		return frame, false
	}
	budget := min(t.credit, cloudlink.MaxMessageBytes-protocol.PtyOutputFrameHeaderBytes(t.id))
	var data []byte
	var seq uint32
	for len(t.pending) > 0 && t.pending[0].output != nil && len(data) < budget {
		chunk := &t.pending[0]
		take := min(len(chunk.output), budget-len(data))
		data = append(data, chunk.output[:take]...)
		seq = chunk.seq
		if take == len(chunk.output) {
			t.pending = t.pending[1:]
		} else {
			chunk.output = chunk.output[take:]
		}
	}
	t.pendingLen -= len(data)
	t.credit -= len(data)
	frame, _ := protocol.EncodePtyOutputFrame(t.id, seq, data)
	return frame, false
}

func (d *Daemon) homeGatewaySendScreen(terminal *homeGatewayTerminal) bool {
	terminal.mu.Lock()
	asked := terminal.screensAsked
	terminal.mu.Unlock()
	snapshot, err := Call[protocol.GetScreenSnapshotResultMessage](context.Background(), d.router(), protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal.id})
	pausepoint.At(pausepoint.HomeGatewayScreenCaptured)
	if err == nil && !snapshot.Success {
		err = errors.New(protocol.Deref(snapshot.Error))
	}
	var ansi []byte
	if err == nil {
		ansi, err = base64.StdEncoding.DecodeString(protocol.Deref(snapshot.ScreenSnapshot))
	}
	screen := homegateway.TerminalScreen{Event: "terminal_screen", TerminalID: string(terminal.id), Cols: snapshotSize(snapshot.ScreenCols, snapshot.Cols), Rows: snapshotSize(snapshot.ScreenRows, snapshot.Rows), Ansi: protocol.Deref(snapshot.ScreenSnapshot), Seq: protocol.Deref(snapshot.LastSeq)}
	frame, _ := json.Marshal(screen)
	if err == nil && len(frame) > cloudlink.MaxMessageBytes {
		err = fmt.Errorf("its screen is %d bytes as a gateway message, past the relay's %d-byte message", len(frame), cloudlink.MaxMessageBytes)
	}
	if err != nil {
		terminal.stopFlushing()
		d.homeGatewayTerminalError(terminal.conn, terminal.id, "could not read the terminal's screen: "+err.Error())
		d.homeGatewayEnd(terminal)
		return false
	}
	terminal.mu.Lock()
	if terminal.screensAsked != asked {
		terminal.mu.Unlock()
		return true
	}
	floor := uint32(protocol.Deref(snapshot.LastSeq))
	terminal.floor = floor
	terminal.screensCovered = asked
	terminal.credit -= len(ansi)
	kept := terminal.pending[:0]
	terminal.pendingLen = 0
	for _, chunk := range terminal.pending {
		if chunk.output != nil && chunk.seq <= floor {
			continue
		}
		kept = append(kept, chunk)
		terminal.pendingLen += len(chunk.output)
	}
	terminal.pending = kept
	terminal.mu.Unlock()
	if err := terminal.send(frame); err != nil {
		terminal.stopFlushing()
		return false
	}
	return true
}

var errTerminalDetached = errors.New("the phone closed this terminal")

func (t *homeGatewayTerminal) send(frame []byte) error {
	t.conn.sendMu.Lock()
	defer t.conn.sendMu.Unlock()
	if t.isDetached() {
		return errTerminalDetached
	}
	return t.conn.stream.Send(frame)
}

func snapshotSize(screen, terminal *int) int {
	if size := protocol.Deref(screen); size > 0 {
		return size
	}
	return protocol.Deref(terminal)
}
