package testworld

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/config"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

func (p *Peer) QuickCapture(msg any) (*protocol.QuickCaptureResult, error) {
	return awaitResult[protocol.QuickCaptureResult](p, msg, protocol.EventQuickCaptureResult)
}

func (p *Peer) Attachment(msg any) (*protocol.AttachmentResult, error) {
	return awaitResult[protocol.AttachmentResult](p, msg, protocol.EventAttachmentResult)
}

type resultFrame[R any] struct {
	RequestID string  `json:"request_id"`
	Success   bool    `json:"success"`
	Error     *string `json:"error"`
	ErrorCode *string `json:"error_code"`
	Result    *R      `json:"result"`
}

func awaitResult[R any](p *Peer, msg any, event string) (*R, error) {
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
			if frame.consumed || frame.event != event {
				continue
			}
			var response resultFrame[R]
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
			return nil, fmt.Errorf("await %s %s: %w", event, id, ctx.Err())
		}
	}
}

func (w *World) TrustedApp(profileID ...string) *Peer {
	w.T.Helper()
	return w.TrustedAppSaying(func(*protocol.ClientHelloMessage) {}, profileID...)
}

// TrustedAppSaying is TrustedApp with its hello changed by say first, such as to send the app's pid.
func (w *World) TrustedAppSaying(say func(*protocol.ClientHelloMessage), profileID ...string) *Peer {
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
	hello := protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: protocol.ClientKindNativeApp, Version: "protocol-" + protocol.ProtocolVersion, Capabilities: []string{protocol.CapabilityBinaryPtyOutput, protocol.CapabilityKittyImages}, ClientToken: protocol.Ptr(strings.TrimSpace(string(clientToken))), BrowserHostToken: protocol.Ptr(strings.TrimSpace(string(token)))}
	if len(profileID) != 0 {
		hello.ProfileID = protocol.Ptr(profileID[0])
	}
	say(&hello)
	p := w.Connect(hello, nil)
	p.Initial = Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}
