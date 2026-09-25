package daemon_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

const hangGuard = 10 * time.Second

type frame struct {
	event    string
	raw      json.RawMessage
	consumed bool
}

type peer struct {
	t        *testing.T
	conn     *websocket.Conn
	mu       sync.Mutex
	grew     chan struct{}
	frames   []frame
	screens  map[string][]byte
	attached map[string]bool
	closeErr error
	initial  protocol.InitialStateMessage
}

func newPeer(t *testing.T, conn *websocket.Conn) *peer {
	p := &peer{
		t:        t,
		conn:     conn,
		grew:     make(chan struct{}),
		screens:  map[string][]byte{},
		attached: map[string]bool{},
	}
	go p.read()
	return p
}

func (p *peer) read() {
	for {
		kind, data, err := p.conn.Read(context.Background())
		p.mu.Lock()
		if err != nil {
			p.closeErr = err
			close(p.grew)
			p.mu.Unlock()
			return
		}
		if kind == websocket.MessageBinary {
			p.recordOutput(data)
		} else {
			p.recordEvent(data)
		}
		close(p.grew)
		p.grew = make(chan struct{})
		p.mu.Unlock()
	}
}

func (p *peer) recordOutput(data []byte) {
	sessionID, _, output, err := protocol.DecodePtyOutputFrame(data)
	if err != nil {
		return
	}
	p.screens[sessionID] = append(p.screens[sessionID], output...)
}

func (p *peer) recordEvent(data []byte) {
	var envelope struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		p.t.Errorf("daemon sent a frame that is not JSON: %v: %s", err, data)
		return
	}
	p.frames = append(p.frames, frame{event: envelope.Event, raw: data})
	if envelope.Event == protocol.EventAttachResult {
		p.recordSnapshot(data)
	}
}

func (p *peer) recordSnapshot(data []byte) {
	var result protocol.AttachResultMessage
	if err := json.Unmarshal(data, &result); err != nil || !result.Success {
		return
	}
	p.attached[result.ID] = true
	p.screens[result.ID] = nil
	if result.Snapshot == nil {
		return
	}
	screen, err := base64.StdEncoding.DecodeString(result.Snapshot.SnapshotB64)
	if err != nil {
		p.t.Errorf("attach snapshot for %s is not base64: %v", result.ID, err)
		return
	}
	p.screens[result.ID] = screen
}

func (p *peer) send(cmd any) {
	p.t.Helper()
	payload, err := json.Marshal(cmd)
	if err != nil {
		p.t.Fatalf("encode %T: %v", cmd, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		p.t.Fatalf("send %s: %v", payload, err)
	}
}

func (p *peer) closed() websocket.CloseError {
	p.t.Helper()
	var closeErr error
	p.until(func() string { return "the daemon to close the connection" }, func() (bool, error) {
		closeErr = p.closeErr
		return closeErr != nil, nil
	})
	var status websocket.CloseError
	if !errors.As(closeErr, &status) {
		p.t.Fatalf("the connection dropped without a close frame: %v", closeErr)
	}
	return status
}

func (p *peer) events() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.frames))
	for _, f := range p.frames {
		names = append(names, f.event)
	}
	return names
}

func (p *peer) close() {
	_ = p.conn.Close(websocket.StatusNormalClosure, "")
}

func (p *peer) typeLine(sessionID, text string) {
	p.t.Helper()
	p.attach(sessionID)
	p.send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: sessionID, Data: text + "\r"})
}

func (p *peer) awaitScreen(sessionID, text string) {
	p.t.Helper()
	p.attach(sessionID)
	p.until(func() string {
		return "the screen of " + sessionID + " to show " + text + "; it shows " + strconv.Quote(string(p.screens[sessionID]))
	}, func() (bool, error) {
		return bytes.Contains(p.screens[sessionID], []byte(text)), nil
	})
}

func (p *peer) attach(sessionID string) {
	p.t.Helper()
	p.mu.Lock()
	attached := p.attached[sessionID]
	p.mu.Unlock()
	if attached {
		return
	}
	result := request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: sessionID},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == sessionID })
	if !result.Success {
		p.t.Fatalf("attach %s refused: %s", sessionID, protocol.Deref(result.Error))
	}
}

func (p *peer) until(awaiting func() string, check func() (bool, error)) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	for {
		p.mu.Lock()
		done, err := check()
		grew, closeErr, described := p.grew, p.closeErr, awaiting()
		p.mu.Unlock()
		switch {
		case err != nil:
			p.t.Fatalf("while awaiting %s: %v", described, err)
		case done:
			return
		case closeErr != nil:
			p.t.Fatalf("connection closed (%v) while awaiting %s", closeErr, described)
		}
		select {
		case <-grew:
		case <-ctx.Done():
			p.mu.Lock()
			described = awaiting()
			p.mu.Unlock()
			p.t.Fatalf("still awaiting %s after %s", described, hangGuard)
		}
	}
}

func await[T any](p *peer, event string, match func(T) bool) T {
	p.t.Helper()
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

func request[T any](p *peer, cmd any, event string, match func(T) bool) T {
	p.t.Helper()
	p.send(cmd)
	return await(p, event, match)
}

func refused(p *peer) protocol.WebSocketEvent {
	p.t.Helper()
	return await[protocol.WebSocketEvent](p, protocol.EventCommandError, nil)
}

func awaitSession(p *peer, id string, match func(protocol.Session) bool) protocol.Session {
	p.t.Helper()
	var found protocol.Session
	p.take("an update of session "+id, func(f frame) (bool, error) {
		var carrier struct {
			Session  *protocol.Session  `json:"session"`
			Sessions []protocol.Session `json:"sessions"`
		}
		if err := json.Unmarshal(f.raw, &carrier); err != nil {
			return false, nil
		}
		if carrier.Session != nil {
			carrier.Sessions = append(carrier.Sessions, *carrier.Session)
		}
		for _, s := range carrier.Sessions {
			if s.ID == id && match(s) {
				found = s
				return true, nil
			}
		}
		return false, nil
	})
	return found
}

func (p *peer) take(awaiting string, accept func(frame) (bool, error)) {
	p.t.Helper()
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
