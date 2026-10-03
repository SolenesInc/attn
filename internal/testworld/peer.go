package testworld

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type frame struct {
	event    string
	raw      json.RawMessage
	consumed bool
	// took names the sessions AwaitSession accepted from a frame listing several, so each can still take it.
	took map[string]bool
}

type sequencedOutput struct {
	seq  uint32
	data []byte
}

type Peer struct {
	T         testing.TB
	Initial   protocol.InitialStateMessage
	conn      *websocket.Conn
	mu        sync.Mutex
	grew      chan struct{}
	frames    []frame
	screens   map[string][]byte
	received  map[string][]sequencedOutput
	watermark map[string]uint32
	empties   map[string]int
	attached  map[string]bool
	panes     map[string][]protocol.WorkspaceLayoutPane
	closeErr  error
	closing   bool
}

func newPeer(t testing.TB, conn *websocket.Conn) *Peer {
	p := &Peer{
		T:         t,
		conn:      conn,
		grew:      make(chan struct{}),
		screens:   map[string][]byte{},
		received:  map[string][]sequencedOutput{},
		watermark: map[string]uint32{},
		empties:   map[string]int{},
		attached:  map[string]bool{},
		panes:     map[string][]protocol.WorkspaceLayoutPane{},
	}
	go p.read()
	return p
}

func (p *Peer) read() {
	for {
		kind, data, err := p.conn.Read(context.Background())
		p.mu.Lock()
		if err != nil {
			p.closeErr = err
			close(p.grew)
			p.mu.Unlock()
			return
		}
		switch {
		case p.closing:
		case kind == websocket.MessageBinary:
			p.recordOutput(data)
		default:
			p.recordEvent(data)
		}
		close(p.grew)
		p.grew = make(chan struct{})
		p.mu.Unlock()
	}
}

func (p *Peer) recordOutput(data []byte) {
	sessionID, seq, output, err := protocol.DecodePtyOutputFrame(data)
	if err != nil {
		return
	}
	if len(output) == 0 {
		p.empties[sessionID]++
	}
	p.received[sessionID] = append(p.received[sessionID], sequencedOutput{seq: seq, data: output})
	if p.attached[sessionID] {
		p.applyOutput(sessionID, seq, output)
	}
}

func (p *Peer) applyOutput(sessionID string, seq uint32, output []byte) {
	if watermark, ok := p.watermark[sessionID]; ok {
		if seq <= watermark {
			return
		}
		p.watermark[sessionID] = seq
	}
	p.screens[sessionID] = append(p.screens[sessionID], output...)
}

func (p *Peer) recordEvent(data []byte) {
	var envelope struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		p.T.Errorf("daemon sent a frame that is not JSON: %v: %s", err, data)
		return
	}
	p.frames = append(p.frames, frame{event: envelope.Event, raw: data})
	switch envelope.Event {
	case protocol.EventAttachResult:
		p.recordSnapshot(data)
	case protocol.EventGetScreenSnapshotResult:
		p.recordScreenSnapshot(data)
	case protocol.EventInitialState, protocol.EventWorkspaceLayout, protocol.EventWorkspaceLayoutUpdated, protocol.EventWorkspaceUnregistered:
		p.recordPanes(envelope.Event, data)
	}
}

func (p *Peer) recordPanes(event string, data []byte) {
	var carrier struct {
		Workspaces      []protocol.Workspace      `json:"workspaces"`
		Workspace       *protocol.Workspace       `json:"workspace"`
		WorkspaceLayout *protocol.WorkspaceLayout `json:"workspace_layout"`
	}
	if err := json.Unmarshal(data, &carrier); err != nil {
		return
	}
	switch {
	case event == protocol.EventInitialState:
		for _, workspace := range carrier.Workspaces {
			if workspace.Layout != nil {
				p.panes[workspace.ID] = workspace.Layout.Panes
			}
		}
	case event == protocol.EventWorkspaceUnregistered && carrier.Workspace != nil:
		delete(p.panes, carrier.Workspace.ID)
	case carrier.WorkspaceLayout != nil:
		p.panes[carrier.WorkspaceLayout.WorkspaceID] = carrier.WorkspaceLayout.Panes
	}
}

// Terminal names the terminal a session's pane places, as the layouts this peer received show it.
// It waits for a layout that places the session.
func (p *Peer) Terminal(sessionID string) string {
	p.T.Helper()
	var pane protocol.WorkspaceLayoutPane
	p.until(func() string { return "a pane that shows session " + sessionID }, func() (bool, error) {
		var ok bool
		pane, ok = p.paneShowingLocked(sessionID)
		return ok, nil
	})
	return protocol.Deref(pane.RuntimeID)
}

// AwaitPanes waits until the panes of every layout this peer received, as it last heard them, match.
func (p *Peer) AwaitPanes(awaiting string, match func([]protocol.WorkspaceLayoutPane) bool) {
	p.T.Helper()
	p.until(func() string { return awaiting }, func() (bool, error) {
		var all []protocol.WorkspaceLayoutPane
		for _, panes := range p.panes {
			all = append(all, panes...)
		}
		return match(all), nil
	})
}

func (p *Peer) paneShowing(sessionID string) (protocol.WorkspaceLayoutPane, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paneShowingLocked(sessionID)
}

func (p *Peer) paneShowingLocked(sessionID string) (protocol.WorkspaceLayoutPane, bool) {
	for _, panes := range p.panes {
		for _, pane := range panes {
			if protocol.Deref(pane.SessionID) == sessionID && protocol.Deref(pane.RuntimeID) != "" {
				return pane, true
			}
		}
	}
	return protocol.WorkspaceLayoutPane{}, false
}

func (p *Peer) recordSnapshot(data []byte) {
	var result protocol.AttachResultMessage
	if err := json.Unmarshal(data, &result); err != nil || !result.Success {
		return
	}
	p.attached[result.ID] = true
	if result.Snapshot == nil {
		p.seed(result.ID, nil, nil)
		return
	}
	lastSeq := uint32(protocol.Deref(result.LastSeq))
	p.seed(result.ID, p.decodeScreen(result.ID, result.Snapshot.SnapshotB64), &lastSeq)
}

func (p *Peer) recordScreenSnapshot(data []byte) {
	var result protocol.GetScreenSnapshotResultMessage
	if err := json.Unmarshal(data, &result); err != nil || !result.Success {
		return
	}
	lastSeq := uint32(protocol.Deref(result.LastSeq))
	p.seed(result.ID, p.decodeScreen(result.ID, protocol.Deref(result.ScreenSnapshot)), &lastSeq)
}

func (p *Peer) decodeScreen(sessionID, encoded string) []byte {
	screen, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		p.T.Errorf("snapshot of %s is not base64: %v", sessionID, err)
	}
	return screen
}

func (p *Peer) seed(sessionID string, screen []byte, lastSeq *uint32) {
	p.screens[sessionID] = screen
	delete(p.watermark, sessionID)
	if lastSeq != nil {
		p.watermark[sessionID] = *lastSeq
	}
	for _, output := range p.received[sessionID] {
		p.applyOutput(sessionID, output.seq, output.data)
	}
}

func (p *Peer) Send(cmd any) {
	p.T.Helper()
	payload, err := json.Marshal(cmd)
	if err != nil {
		p.T.Fatalf("encode %T: %v", cmd, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		p.T.Fatalf("send %s: %v", payload, err)
	}
}

func (p *Peer) Closed() websocket.CloseError {
	p.T.Helper()
	var closeErr error
	p.until(func() string { return "the daemon to close the connection" }, func() (bool, error) {
		closeErr = p.closeErr
		return closeErr != nil, nil
	})
	var status websocket.CloseError
	if !errors.As(closeErr, &status) {
		p.T.Fatalf("the connection dropped without a close frame: %v", closeErr)
	}
	return status
}

func (p *Peer) Received() []protocol.WebSocketEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make([]protocol.WebSocketEvent, 0, len(p.frames))
	for _, f := range p.frames {
		var e protocol.WebSocketEvent
		if err := json.Unmarshal(f.raw, &e); err != nil {
			p.T.Errorf("event %s does not decode: %v", f.event, err)
			continue
		}
		events = append(events, e)
	}
	return events
}

func (p *Peer) Close() {
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()
	_ = p.conn.Close(websocket.StatusNormalClosure, "")
}

func (p *Peer) TypeLine(sessionID, text string) {
	p.T.Helper()
	p.TypeLineIn(p.Terminal(sessionID), text)
}

// TypeLineIn types into one terminal, for a session that several panes show.
func (p *Peer) TypeLineIn(terminal, text string) {
	p.T.Helper()
	p.attach(terminal)
	p.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: text + "\r"})
}

func (p *Peer) AwaitScreen(sessionID, text string) {
	p.T.Helper()
	terminal := p.Terminal(sessionID)
	p.attach(terminal)
	p.until(func() string {
		return "the screen of " + sessionID + " to show " + text + "; it shows " + strconv.Quote(string(p.screens[terminal]))
	}, func() (bool, error) {
		return bytes.Contains(p.screens[terminal], []byte(text)), nil
	})
}

func (p *Peer) Screen(sessionID string) []byte {
	terminal := p.Terminal(sessionID)
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.screens[terminal]...)
}

const screenLogTailBytes = 4096

func (p *Peer) LogScreens() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(p.screens)) {
		screen := p.screens[id]
		tail := screen[max(0, len(screen)-screenLogTailBytes):]
		p.T.Logf("output this peer received for terminal %s (last %d of %d bytes): %q", id, len(tail), len(screen), tail)
	}
}

func (p *Peer) EmptyOutputs(sessionID string) int {
	terminal := p.Terminal(sessionID)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.empties[terminal]
}

func (p *Peer) attach(terminal string) {
	p.T.Helper()
	p.mu.Lock()
	attached := p.attached[terminal]
	p.mu.Unlock()
	if attached {
		return
	}
	result := Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
	if !result.Success {
		p.T.Fatalf("attach %s refused: %s", terminal, protocol.Deref(result.Error))
	}
}

func (p *Peer) until(awaiting func() string, check func() (bool, error)) {
	p.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	for {
		p.mu.Lock()
		done, err := check()
		grew, closeErr, described := p.grew, p.closeErr, awaiting()
		p.mu.Unlock()
		switch {
		case err != nil:
			p.T.Fatalf("while awaiting %s: %v", described, err)
		case done:
			return
		case closeErr != nil:
			p.T.Fatalf("connection closed (%v) while awaiting %s", closeErr, described)
		}
		select {
		case <-grew:
		case <-ctx.Done():
			p.mu.Lock()
			described = awaiting()
			p.mu.Unlock()
			p.T.Fatalf("still awaiting %s after %s", described, fakeagent.HangGuard)
		}
	}
}

func Await[T any](p *Peer, event string, match func(T) bool) T {
	p.T.Helper()
	var found T
	p.take(event, func(f frame) (bool, error) {
		if f.event != event {
			return false, nil
		}
		var candidate T
		if err := json.Unmarshal(f.raw, &candidate); err != nil {
			return false, fmt.Errorf("decode %s: %w: %s", event, err, f.raw)
		}
		if match != nil && !match(candidate) {
			return false, nil
		}
		found = candidate
		return true, nil
	})
	return found
}

func AwaitEvent(p *Peer, awaiting string, match func(protocol.WebSocketEvent) bool) protocol.WebSocketEvent {
	p.T.Helper()
	var found protocol.WebSocketEvent
	p.take(awaiting, func(f frame) (bool, error) {
		var candidate protocol.WebSocketEvent
		if err := json.Unmarshal(f.raw, &candidate); err != nil {
			return false, fmt.Errorf("decode %s: %w: %s", f.event, err, f.raw)
		}
		if !match(candidate) {
			return false, nil
		}
		found = candidate
		return true, nil
	})
	return found
}

func Request[T any](p *Peer, cmd any, event string, match func(T) bool) T {
	p.T.Helper()
	p.Send(cmd)
	return Await(p, event, match)
}

func Refused(p *Peer) protocol.WebSocketEvent {
	p.T.Helper()
	return Await[protocol.WebSocketEvent](p, protocol.EventCommandError, nil)
}

func AwaitSession(p *Peer, id string, match func(protocol.Session) bool) protocol.Session {
	p.T.Helper()
	var found protocol.Session
	scanned := 0
	p.until(func() string { return "an update of session " + id }, func() (bool, error) {
		for ; scanned < len(p.frames); scanned++ {
			f := &p.frames[scanned]
			var carrier struct {
				Session  *protocol.Session  `json:"session"`
				Sessions []protocol.Session `json:"sessions"`
			}
			if err := json.Unmarshal(f.raw, &carrier); err != nil {
				continue
			}
			listed := len(carrier.Sessions) > 1
			if f.took[id] || (f.consumed && (!listed || f.took == nil)) {
				continue
			}
			if carrier.Session != nil {
				carrier.Sessions = append(carrier.Sessions, *carrier.Session)
			}
			for _, s := range carrier.Sessions {
				if s.ID == id && match(s) {
					found = s
					if listed {
						if f.took == nil {
							f.took = make(map[string]bool)
						}
						f.took[id] = true
					}
					f.consumed = true
					return true, nil
				}
			}
			if f.event == protocol.EventCommandError && !f.consumed {
				return false, fmt.Errorf("the daemon refused a command: %s", f.raw)
			}
		}
		return false, nil
	})
	return found
}

func AwaitStateAfter(p *Peer, previous protocol.Session, match func(protocol.Session) bool) protocol.Session {
	p.T.Helper()
	after := stateSince(p.T, previous)
	return AwaitSession(p, previous.ID, func(s protocol.Session) bool {
		return stateSince(p.T, s).After(after) && match(s)
	})
}

func stateSince(t testing.TB, s protocol.Session) time.Time {
	t.Helper()
	since, err := time.Parse(time.RFC3339Nano, s.StateSince)
	if err != nil {
		t.Fatalf("session %s state_since %q: %v", s.ID, s.StateSince, err)
	}
	return since
}

func (p *Peer) take(awaiting string, accept func(frame) (bool, error)) {
	p.T.Helper()
	scanned := 0
	p.until(func() string { return awaiting }, func() (bool, error) {
		for ; scanned < len(p.frames); scanned++ {
			f := &p.frames[scanned]
			if f.consumed {
				continue
			}
			accepted, err := accept(*f)
			if err != nil {
				return false, err
			}
			if accepted {
				f.consumed = true
				return true, nil
			}
			if f.event == protocol.EventCommandError {
				return false, fmt.Errorf("the daemon refused a command: %s", f.raw)
			}
		}
		return false, nil
	})
}
