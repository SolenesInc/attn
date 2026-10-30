package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/stepclock"
	"golang.org/x/crypto/ssh"
)

type Pin struct {
	HostKey  string
	DaemonID string
}

func (p Pin) Fingerprint() string {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(key)
}

type DialConfig struct {
	Conn         net.Conn
	Pin          *Pin
	Self         Identity
	CheckHostKey func(string) *Refusal
	Handlers     Handlers
}

func Dial(ctx context.Context, cfg DialConfig) (*Conn, *Pin, error) {
	handshakeCtx, cancel := context.WithCancelCause(ctx)
	deadline := stepclock.AfterFunc(AnswerWithin, func() { cancel(context.DeadlineExceeded) })
	defer deadline.Stop()
	defer cancel(context.Canceled)
	ready, seen, err := handshake(handshakeCtx, cfg)
	var refusal *Refusal
	if err != nil && !errors.As(err, &refusal) && errors.Is(context.Cause(handshakeCtx), context.DeadlineExceeded) {
		err = fmt.Errorf("bridge to %s: no answer within %s: %w", cfg.Conn.RemoteAddr().String(), AnswerWithin, context.DeadlineExceeded)
	}
	if err != nil {
		return nil, seen, err
	}
	return newConn(ctx, ready.transport, ready.channels, cfg.Handlers, ready.control, ready.decoder, ready.peer, ready.drained), seen, nil
}

type authenticatedPeer struct {
	transport ssh.Conn
	channels  <-chan ssh.NewChannel
	control   ssh.Channel
	decoder   *json.Decoder
	peer      Identity
	drained   <-chan struct{}
}

func handshake(handshakeCtx context.Context, cfg DialConfig) (*authenticatedPeer, *Pin, error) {
	raw := cfg.Conn
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
		}
	}()
	if err := raw.SetDeadline(time.Now().Add(AnswerWithin)); err != nil {
		return nil, nil, err
	}
	stopRaw := context.AfterFunc(handshakeCtx, func() { _ = raw.Close() })
	defer stopRaw()
	seen := &Pin{}
	var pinErr *Refusal
	sshConfig := &ssh.ClientConfig{User: "attn", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if cfg.Pin == nil && cfg.CheckHostKey != nil {
			if pinErr = cfg.CheckHostKey(seen.HostKey); pinErr != nil {
				return pinErr
			}
		}
		if cfg.Pin != nil && seen.HostKey != cfg.Pin.HostKey {
			pinErr = &Refusal{Reason: "identity", Detail: fmt.Sprintf("outpost %s (%s) changed host key: pinned %s, received %s; if you expect that, re-pair it in Settings → Outposts", cfg.Pin.DaemonID, cfg.Conn.RemoteAddr().String(), cfg.Pin.Fingerprint(), seen.Fingerprint())}
			return pinErr
		}
		return nil
	}}
	transport, channels, requests, err := ssh.NewClientConn(raw, cfg.Conn.RemoteAddr().String(), sshConfig)
	if err != nil {
		if pinErr != nil {
			return nil, seen, pinErr
		}
		return nil, seen, err
	}
	requestDrain := make(chan struct{})
	go func() { defer close(requestDrain); ssh.DiscardRequests(requests) }()
	closeTransport := func() {
		_ = transport.Close()
		<-requestDrain
	}
	defer func() {
		if !success {
			closeTransport()
		}
	}()
	control, channelRequests, err := transport.OpenChannel(controlChannel, nil)
	if err != nil {
		return nil, seen, err
	}
	go ssh.DiscardRequests(channelRequests)
	greeting := greeting(cfg.Self)
	if err := json.NewEncoder(control).Encode(greeting); err != nil {
		return nil, seen, err
	}
	decoder := json.NewDecoder(control)
	var answer hello
	err = decoder.Decode(&answer)
	if errors.Is(err, io.EOF) {
		err = fmt.Errorf("outpost at %s closed before its bridge hello; if it runs an older build, choose Update in Settings → Outposts at the home: %w", cfg.Conn.RemoteAddr().String(), err)
	}
	if err == nil {
		err = answer.check()
	}
	if err == nil {
		seen.DaemonID = answer.DaemonID
		if cfg.Pin != nil && cfg.Pin.DaemonID != seen.DaemonID {
			err = &Refusal{Reason: "identity", Detail: fmt.Sprintf("outpost at %s changed daemon id: pinned %s, received %s; if you expect that, re-pair it in Settings → Outposts", cfg.Conn.RemoteAddr().String(), cfg.Pin.DaemonID, seen.DaemonID)}
		} else if answer.Refused != nil {
			answer.Refused.Peer = &answer.Identity
			err = answer.Refused
		} else if refusal := instanceRefusal(cfg.Self, answer.Identity); refusal != nil {
			err = refusal
		} else if refusal := buildRefusal(cfg.Self, answer.Identity); refusal != nil {
			err = refusal
		}
	}
	if err != nil {
		return nil, seen, err
	}
	if handshakeCtx.Err() != nil {
		return nil, seen, handshakeCtx.Err()
	}
	if !stopRaw() && handshakeCtx.Err() != nil {
		return nil, seen, handshakeCtx.Err()
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		return nil, seen, err
	}
	success = true
	return &authenticatedPeer{transport: transport, channels: channels, control: control, decoder: decoder, peer: answer.Identity, drained: requestDrain}, seen, nil
}
