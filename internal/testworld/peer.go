package testworld

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

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type frame struct {
	event    string
	raw      json.RawMessage
	consumed bool
}

type Peer struct {
	T        testing.TB
	Initial  protocol.InitialStateMessage
	conn     *websocket.Conn
	mu       sync.Mutex
	grew     chan struct{}
	frames   []frame
	screens  map[string][]byte
	attached map[string]bool
	closeErr error
}

func newPeer(t testing.TB, conn *websocket.Conn) *Peer {
	p := &Peer{
		T:        t,
		conn:     conn,
		grew:     make(chan struct{}),
		screens:  map[string][]byte{},
		attached: map[string]bool{},
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

func (p *Peer) recordOutput(data []byte) {
	sessionID, _, output, err := protocol.DecodePtyOutputFrame(data)
	if err != nil {
		return
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
	if envelope.Event == protocol.EventAttachResult {
		p.recordSnapshot(data)
	}
}

func (p *Peer) recordSnapshot(data []byte) {
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
		p.T.Errorf("attach snapshot for %s is not base64: %v", result.ID, err)
		return
	}
	p.screens[result.ID] = screen
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

func (p *Peer) Events() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.frames))
	for _, f := range p.frames {
		names = append(names, f.event)
	}
	return names
}

func (p *Peer) Close() {
	_ = p.conn.Close(websocket.StatusNormalClosure, "")
}

func (p *Peer) TypeLine(sessionID, text string) {
	p.T.Helper()
	p.attach(sessionID)
	p.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: sessionID, Data: text + "\r"})
}

func (p *Peer) AwaitScreen(sessionID, text string) {
	p.T.Helper()
	p.attach(sessionID)
	p.until(func() string {
		return "the screen of " + sessionID + " to show " + text + "; it shows " + strconv.Quote(string(p.screens[sessionID]))
	}, func() (bool, error) {
		return bytes.Contains(p.screens[sessionID], []byte(text)), nil
	})
}

func (p *Peer) attach(sessionID string) {
	p.T.Helper()
	p.mu.Lock()
	attached := p.attached[sessionID]
	p.mu.Unlock()
	if attached {
		return
	}
	result := Request(p, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: sessionID},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == sessionID })
	if !result.Success {
		p.T.Fatalf("attach %s refused: %s", sessionID, protocol.Deref(result.Error))
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
