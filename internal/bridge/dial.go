package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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
	Address      string
	Pin          *Pin
	Self         Identity
	AgentSocket  string
	DialContext  func(context.Context, string, string) (net.Conn, error)
	CheckHostKey func(string) *Refusal
	Handlers     Handlers
	update       bool
}

func Dial(ctx context.Context, cfg DialConfig) (*Conn, *Pin, error) {
	handshakeCtx, cancel := context.WithTimeout(ctx, LostAfter)
	defer cancel()
	ready, seen, err := handshake(handshakeCtx, cfg)
	var refusal *Refusal
	if err != nil && !errors.As(err, &refusal) && errors.Is(handshakeCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("bridge to %s: no answer within %s: %w", cfg.Address, LostAfter, context.DeadlineExceeded)
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
	target    *UpdateTarget
	drained   <-chan struct{}
	raw       net.Conn
}

func handshake(handshakeCtx context.Context, cfg DialConfig) (*authenticatedPeer, *Pin, error) {
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	if cfg.AgentSocket == "" {
		return nil, nil, &Refusal{Reason: "auth", Detail: "bridge authentication: no SSH_AUTH_SOCK; start an ssh-agent and load a key"}
	}
	agentConn, err := dial(handshakeCtx, "unix", cfg.AgentSocket)
	if err != nil {
		return nil, nil, &Refusal{Reason: "auth", Detail: fmt.Sprintf("bridge authentication: agent unreachable at %s: %v", cfg.AgentSocket, err)}
	}
	defer agentConn.Close()
	stopAgent := context.AfterFunc(handshakeCtx, func() { _ = agentConn.Close() })
	defer stopAgent()
	signers, err := agent.NewClient(agentConn).Signers()
	if err != nil {
		return nil, nil, &Refusal{Reason: "auth", Detail: fmt.Sprintf("bridge authentication: cannot list agent keys: %v", err)}
	}
	if len(signers) == 0 {
		return nil, nil, &Refusal{Reason: "auth", Detail: "bridge authentication: agent has no keys; load a key with ssh-add"}
	}
	raw, err := dial(handshakeCtx, "tcp", cfg.Address)
	if err != nil {
		return nil, nil, err
	}
	if cfg.update {
		raw = &updateActivityConn{Conn: raw}
	}
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
		}
	}()
	deadline, _ := handshakeCtx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, nil, err
	}
	stopRaw := context.AfterFunc(handshakeCtx, func() { _ = raw.Close() })
	defer stopRaw()
	seen := &Pin{}
	var pinErr *Refusal
	sshConfig := &ssh.ClientConfig{User: "attn", Auth: []ssh.AuthMethod{ssh.PublicKeys(signers...)}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if cfg.Pin == nil && cfg.CheckHostKey != nil {
			if pinErr = cfg.CheckHostKey(seen.HostKey); pinErr != nil {
				return pinErr
			}
		}
		if cfg.Pin != nil && seen.HostKey != cfg.Pin.HostKey {
			pinErr = &Refusal{Reason: "identity", Detail: fmt.Sprintf("outpost %s (%s) changed host key: pinned %s, received %s; if you expect that, re-pair it in Settings → Outposts", cfg.Pin.DaemonID, cfg.Address, cfg.Pin.Fingerprint(), seen.Fingerprint())}
			return pinErr
		}
		return nil
	}}
	transport, channels, requests, err := ssh.NewClientConn(raw, cfg.Address, sshConfig)
	if err != nil {
		if pinErr != nil {
			return nil, seen, pinErr
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, seen, &Refusal{Reason: "auth", Detail: fmt.Sprintf("bridge to %s: no key accepted by outpost authorized_keys: %v", cfg.Address, err)}
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
	if cfg.update {
		greeting.Update = 1
	}
	if err := json.NewEncoder(control).Encode(greeting); err != nil {
		return nil, seen, err
	}
	decoder := json.NewDecoder(control)
	var answer hello
	err = decoder.Decode(&answer)
	if err == nil {
		err = answer.check()
	}
	if err == nil {
		seen.DaemonID = answer.DaemonID
		if cfg.Pin != nil && cfg.Pin.DaemonID != seen.DaemonID {
			err = &Refusal{Reason: "identity", Detail: fmt.Sprintf("outpost at %s changed daemon id: pinned %s, received %s; if you expect that, re-pair it in Settings → Outposts", cfg.Address, cfg.Pin.DaemonID, seen.DaemonID)}
		} else if answer.Refused != nil {
			answer.Refused.Peer = &answer.Identity
			err = answer.Refused
			if cfg.update && answer.Refused.Reason == "build" {
				err = fmt.Errorf("outpost build %s predates updates from the home; install this build by hand once: %w", answer.Build, err)
			}
		} else if cfg.update && (answer.Update != 1 || answer.Target == nil) {
			err = fmt.Errorf("outpost build %s predates updates from the home; install this build by hand once", answer.Build)
		} else if refusal := buildRefusal(cfg.Self, answer.Identity); !cfg.update && refusal != nil {
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
	return &authenticatedPeer{transport: transport, channels: channels, control: control, decoder: decoder, peer: answer.Identity, target: answer.Target, drained: requestDrain, raw: raw}, seen, nil
}
