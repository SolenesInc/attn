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
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

const gatewayTerminalAPI = 4

// gatewayOutputCoalesce batches PTY output so a streaming terminal sends about 60 frames a second, inside the
// relay's measured 4,000 messages a second for a whole home (plan F2).
const gatewayOutputCoalesce = 16 * time.Millisecond

// gatewayPendingTripwire bounds output held for a device that stopped granting credit. Past it, a fresh screen
// replaces the backlog: a device that far behind gains nothing from replaying it.
const gatewayPendingTripwire = 1 << 20

type gatewayTerminal struct {
	id   protocol.TerminalID
	conn *gatewayConn

	mu         sync.Mutex
	stream     ptybackend.Stream
	pending    []gatewayChunk
	pendingLen int
	floor      uint32
	credit     int
	needScreen bool
	flushing   bool
	detached   bool
}

type gatewayChunk struct {
	output []byte
	seq    uint32
	resize *gateway.TerminalResized
	ended  bool
}

func (c *gatewayConn) commandOrder() *terminalOrder { return &c.order }

func (d *Daemon) gatewayCommand(conn *gatewayConn, cmd string, frame []byte) bool {
	switch cmd {
	case "terminal_attach":
		if message, ok := decodeGatewayCommand[gateway.TerminalAttach](d, conn, frame); ok {
			d.gatewayAttach(conn, message)
		}
	case "terminal_credit":
		if message, ok := decodeGatewayCommand[gateway.TerminalCredit](d, conn, frame); ok {
			if terminal := conn.terminal(protocol.TerminalID(message.TerminalID)); terminal != nil {
				d.gatewayGrant(terminal, message.Bytes)
			}
		}
	case "terminal_detach":
		if message, ok := decodeGatewayCommand[gateway.TerminalDetach](d, conn, frame); ok {
			d.gatewayDetach(conn, protocol.TerminalID(message.TerminalID))
		}
	case "terminal_input":
		if message, ok := decodeGatewayCommand[gateway.TerminalInput](d, conn, frame); ok {
			d.gatewayInput(conn, protocol.TerminalID(message.TerminalID), message.Data)
		}
	case "terminal_resize":
		if message, ok := decodeGatewayCommand[gateway.TerminalResize](d, conn, frame); ok {
			d.gatewayRequestSize(conn, protocol.TerminalID(message.TerminalID), message.Cols, message.Rows)
		}
	case "terminal_focus":
		if message, ok := decodeGatewayCommand[gateway.TerminalFocus](d, conn, frame); ok {
			d.claimPtySize(conn, protocol.TerminalID(message.TerminalID))
		}
	case "submit_prompt":
		if message, ok := decodeGatewayCommand[gateway.SubmitPrompt](d, conn, frame); ok {
			d.life.Go("gatewaySubmitPrompt", func() { d.gatewaySubmitPrompt(conn, message) })
		}
	default:
		return false
	}
	return true
}

func decodeGatewayCommand[T any](d *Daemon, conn *gatewayConn, frame []byte) (T, bool) {
	var message T
	if err := json.Unmarshal(frame, &message); err != nil {
		d.logf("gateway: device %s sent %s, which does not decode: %v", conn.device.DeviceID, frame, err)
		return message, false
	}
	return message, true
}

func (c *gatewayConn) terminal(id protocol.TerminalID) *gatewayTerminal {
	c.terminalsMu.Lock()
	defer c.terminalsMu.Unlock()
	return c.terminals[id]
}

func (d *Daemon) gatewayTerminalOfProfile(conn *gatewayConn, terminal protocol.TerminalID) bool {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && session.ProfileID == conn.device.ProfileID
}

func (d *Daemon) gatewayAttach(conn *gatewayConn, message gateway.TerminalAttach) {
	id := protocol.TerminalID(message.TerminalID)
	if !d.gatewayTerminalOfProfile(conn, id) {
		d.gatewayTerminalError(conn, id, fmt.Sprintf("terminal %s is not a session terminal of this pairing's profile", id))
		return
	}
	d.gatewayDetach(conn, id)
	if !d.gatewayRequestSize(conn, id, message.Cols, message.Rows) {
		return
	}
	terminal := &gatewayTerminal{id: id, conn: conn, credit: message.Credit, needScreen: true}
	stream, err := d.gatewayOpenStream(terminal)
	if err != nil {
		d.gatewayTerminalError(conn, id, "could not open the terminal: "+err.Error())
		d.gatewayReleaseSize(conn, id)
		return
	}
	terminal.stream = stream
	conn.terminalsMu.Lock()
	conn.terminals[id] = terminal
	conn.terminalsMu.Unlock()
	if !d.life.Go("gatewayTerminal", func() { d.runGatewayTerminal(terminal, stream) }) {
		d.gatewayDetach(conn, id)
		return
	}
	d.gatewayFlushSoon(terminal, 0)
}

func (d *Daemon) gatewayOpenStream(terminal *gatewayTerminal) (ptybackend.Stream, error) {
	subscriber := fmt.Sprintf("gateway:%s:%p", terminal.conn.device.DeviceID, terminal)
	_, stream, err := d.router().Attach(context.Background(), terminal.id, subscriber, ptybackend.AttachOptions{OmitReplay: true})
	return stream, err
}

func (d *Daemon) gatewayDetach(conn *gatewayConn, id protocol.TerminalID) {
	conn.terminalsMu.Lock()
	terminal := conn.terminals[id]
	delete(conn.terminals, id)
	conn.terminalsMu.Unlock()
	if terminal != nil {
		terminal.detach()
	}
	d.gatewayReleaseSize(conn, id)
}

func (d *Daemon) gatewayDetachAll(conn *gatewayConn) {
	conn.terminalsMu.Lock()
	terminals := conn.terminals
	conn.terminals = map[protocol.TerminalID]*gatewayTerminal{}
	conn.terminalsMu.Unlock()
	for _, terminal := range terminals {
		terminal.detach()
	}
	d.releasePtySizes(conn)
}

func (t *gatewayTerminal) detach() {
	t.mu.Lock()
	t.detached = true
	stream := t.stream
	t.pending = nil
	t.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func (d *Daemon) gatewayReleaseSize(conn *gatewayConn, id protocol.TerminalID) {
	if next, handedOn := d.ptySizes.release(conn, id); handedOn {
		d.applyPtySize(next.owner, id)
	}
}

func (d *Daemon) gatewayRequestSize(conn *gatewayConn, id protocol.TerminalID, cols, rows int) bool {
	if cols <= 0 || rows <= 0 || cols > maxPTYDimValue || rows > maxPTYDimValue {
		d.gatewayTerminalError(conn, id, fmt.Sprintf("invalid terminal size cols=%d rows=%d (expected 1..%d)", cols, rows, maxPTYDimValue))
		return false
	}
	if d.ptySizes.request(conn, protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: id, Cols: cols, Rows: rows}) {
		d.applyPtySize(conn, id)
	}
	return true
}

func (d *Daemon) gatewayInput(conn *gatewayConn, id protocol.TerminalID, data string) {
	if !d.gatewayTerminalOfProfile(conn, id) {
		d.gatewayTerminalError(conn, id, fmt.Sprintf("terminal %s is not a session terminal of this pairing's profile", id))
		return
	}
	d.claimPtySize(conn, id)
	d.sendInTerminalOrder(conn, id, func(answered func()) {
		write := protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: id, Data: data}
		d.router().Send(write, func(err error) {
			answered()
			if err != nil {
				d.gatewayTerminalError(conn, id, "the terminal refused input: "+err.Error())
			}
		})
	})
}

func (d *Daemon) gatewaySubmitPrompt(conn *gatewayConn, message gateway.SubmitPrompt) {
	result := gateway.SubmitResult{Event: "submit_result", RequestID: message.RequestID}
	sessionID := protocol.SessionID(message.SessionID)
	session := d.store.Get(sessionID)
	switch {
	case session == nil || session.ProfileID != conn.device.ProfileID:
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
	_ = gatewaySend(conn, result)
}

func (d *Daemon) gatewayTerminalError(conn *gatewayConn, id protocol.TerminalID, message string) {
	_ = gatewaySend(conn, gateway.TerminalError{Event: "terminal_error", TerminalID: string(id), Message: message})
}

func (d *Daemon) runGatewayTerminal(terminal *gatewayTerminal, stream ptybackend.Stream) {
	for {
		d.gatewayReadStream(terminal, stream)
		if terminal.isDetached() {
			return
		}
		if !d.terminalNeedsResync(d.life.Context(), terminal.id) {
			terminal.queue(gatewayChunk{ended: true})
			d.gatewayFlushSoon(terminal, 0)
			return
		}
		next, err := d.gatewayOpenStream(terminal)
		if err != nil {
			d.gatewayTerminalError(terminal.conn, terminal.id, "lost the terminal and could not reopen it: "+err.Error())
			return
		}
		if !terminal.reopened(next) {
			_ = next.Close()
			return
		}
		stream = next
		d.gatewayFlushSoon(terminal, 0)
	}
}

func (d *Daemon) gatewayReadStream(terminal *gatewayTerminal, stream ptybackend.Stream) {
	defer stream.Close()
	for event := range stream.Events() {
		desync := event.Kind == ptybackend.OutputEventKindDesync
		switch event.Kind {
		case ptybackend.OutputEventKindOutput:
			terminal.output(event.Seq, event.Data)
		case ptybackend.OutputEventKindResize:
			terminal.queue(gatewayChunk{resize: &gateway.TerminalResized{Event: "terminal_resized", TerminalID: string(terminal.id), Cols: int(event.Cols), Rows: int(event.Rows)}})
		}
		if acknowledged, ok := stream.(interface{ Acknowledge() }); ok {
			acknowledged.Acknowledge()
		}
		if desync {
			return
		}
		d.gatewayFlushSoon(terminal, gatewayOutputCoalesce)
	}
}

func (t *gatewayTerminal) isDetached() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.detached
}

func (t *gatewayTerminal) reopened(stream ptybackend.Stream) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detached {
		return false
	}
	t.stream = stream
	t.pending = nil
	t.pendingLen = 0
	t.needScreen = true
	return true
}

func (t *gatewayTerminal) output(seq uint32, data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.detached || (!t.needScreen && seq <= t.floor) {
		return
	}
	if t.pendingLen+len(data) > gatewayPendingTripwire {
		t.pending = nil
		t.pendingLen = 0
		t.needScreen = true
		return
	}
	t.pending = append(t.pending, gatewayChunk{output: data, seq: seq})
	t.pendingLen += len(data)
}

func (t *gatewayTerminal) queue(chunk gatewayChunk) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.detached {
		t.pending = append(t.pending, chunk)
	}
}

func (d *Daemon) gatewayGrant(terminal *gatewayTerminal, bytes int) {
	terminal.mu.Lock()
	terminal.credit += bytes
	terminal.mu.Unlock()
	d.gatewayFlushSoon(terminal, 0)
}

func (d *Daemon) gatewayFlushSoon(terminal *gatewayTerminal, delay time.Duration) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.flushing || !terminal.hasWorkLocked() {
		return
	}
	terminal.flushing = true
	d.life.AfterFunc("gatewayFlush", delay, func() { d.gatewayFlush(terminal) })
}

func (t *gatewayTerminal) hasWorkLocked() bool {
	if t.detached {
		return false
	}
	if t.needScreen {
		return t.credit >= cloudlink.MaxMessageBytes
	}
	return t.credit > 0 && len(t.pending) > 0
}

func (d *Daemon) gatewayFlush(terminal *gatewayTerminal) {
	for {
		terminal.mu.Lock()
		if !terminal.hasWorkLocked() {
			terminal.flushing = false
			terminal.mu.Unlock()
			return
		}
		if terminal.needScreen {
			terminal.mu.Unlock()
			if !d.gatewaySendScreen(terminal) {
				return
			}
			continue
		}
		message, ended := terminal.nextMessageLocked()
		terminal.mu.Unlock()
		if err := terminal.conn.sendRaw(message); err != nil {
			terminal.stopFlushing()
			return
		}
		if ended {
			d.gatewayDetach(terminal.conn, terminal.id)
			return
		}
	}
}

func (t *gatewayTerminal) stopFlushing() {
	t.mu.Lock()
	t.flushing = false
	t.mu.Unlock()
}

func (t *gatewayTerminal) nextMessageLocked() ([]byte, bool) {
	head := t.pending[0]
	switch {
	case head.ended:
		t.pending = t.pending[1:]
		frame, _ := json.Marshal(gateway.TerminalEnded{Event: "terminal_ended", TerminalID: string(t.id)})
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

func (d *Daemon) gatewaySendScreen(terminal *gatewayTerminal) bool {
	snapshot, err := Call[protocol.GetScreenSnapshotResultMessage](context.Background(), d.router(), protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal.id})
	if err == nil && !snapshot.Success {
		err = errors.New(protocol.Deref(snapshot.Error))
	}
	var ansi []byte
	if err == nil {
		ansi, err = base64.StdEncoding.DecodeString(protocol.Deref(snapshot.ScreenSnapshot))
	}
	screen := gateway.TerminalScreen{Event: "terminal_screen", TerminalID: string(terminal.id), Cols: snapshotSize(snapshot.ScreenCols, snapshot.Cols), Rows: snapshotSize(snapshot.ScreenRows, snapshot.Rows), Ansi: protocol.Deref(snapshot.ScreenSnapshot), Seq: protocol.Deref(snapshot.LastSeq)}
	frame, _ := json.Marshal(screen)
	if err == nil && len(frame) > cloudlink.MaxMessageBytes {
		err = fmt.Errorf("its screen is %d bytes as a gateway message, past the relay's %d-byte message", len(frame), cloudlink.MaxMessageBytes)
	}
	if err != nil {
		terminal.stopFlushing()
		d.gatewayTerminalError(terminal.conn, terminal.id, "could not read the terminal's screen: "+err.Error())
		d.gatewayDetach(terminal.conn, terminal.id)
		return false
	}
	terminal.mu.Lock()
	floor := uint32(protocol.Deref(snapshot.LastSeq))
	terminal.floor = floor
	terminal.needScreen = false
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
	if err := terminal.conn.sendRaw(frame); err != nil {
		terminal.stopFlushing()
		return false
	}
	return true
}

func snapshotSize(screen, terminal *int) int {
	if size := protocol.Deref(screen); size > 0 {
		return size
	}
	return protocol.Deref(terminal)
}
