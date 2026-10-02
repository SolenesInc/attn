package testworld

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/config"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

// Capture drives the authenticated app channel and returns disconnects as errors.
func (p *Peer) Capture(msg any) (*protocol.CaptureResult, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	id := rand.Text()
	request["request_id"] = id
	data, err = json.Marshal(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	for {
		p.mu.Lock()
		for i := range p.frames {
			frame := &p.frames[i]
			if frame.consumed || frame.event != protocol.EventCaptureResult {
				continue
			}
			var response protocol.CaptureResultMessage
			if err := json.Unmarshal(frame.raw, &response); err != nil {
				p.mu.Unlock()
				return nil, err
			}
			if response.RequestID != id {
				continue
			}
			frame.consumed = true
			p.mu.Unlock()
			if !response.Success {
				return nil, &client.DaemonError{Code: protocol.Deref(response.ErrorCode), Message: protocol.Deref(response.Error)}
			}
			return response.Result, nil
		}
		grew, closed := p.grew, p.closeErr
		p.mu.Unlock()
		if closed != nil {
			return nil, closed
		}
		select {
		case <-grew:
		case <-ctx.Done():
			return nil, fmt.Errorf("await capture %s: %w", id, ctx.Err())
		}
	}
}

// TrustedApp represents the main Tauri webview, including its private host credential.
func (w *World) TrustedApp() *Peer {
	w.T.Helper()
	path := filepath.Join(w.Dir, "browser-host-token")
	token, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		token = []byte(rand.Text())
		err = os.WriteFile(path, token, 0600)
	}
	if err != nil {
		w.T.Fatalf("prepare trusted app credential: %v", err)
	}
	clientToken, err := os.ReadFile(filepath.Join(w.Dir, config.ClientTokenFile))
	if err != nil {
		w.T.Fatalf("read daemon credential: %v", err)
	}
	p := w.Connect(protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion, Capabilities: []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBinaryPtyOutput, protocol.CapabilityKittyImages}, ClientToken: protocol.Ptr(strings.TrimSpace(string(clientToken))), BrowserHostToken: protocol.Ptr(strings.TrimSpace(string(token)))}, http.Header{"Origin": {"tauri://localhost"}})
	p.Initial = Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}
