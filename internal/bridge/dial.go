package bridge

import (
	"context"
	"encoding/json"
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
}

func Dial(ctx context.Context, cfg DialConfig) (*Conn, *Pin, error) {
	handshakeCtx, cancel := context.WithTimeout(ctx, LostAfter)
	defer cancel()
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
	client := ssh.NewClient(transport, channels, requests)
	defer func() {
		if !success {
			_ = client.Close()
			_ = client.Wait()
		}
	}()
	control, channelRequests, err := client.OpenChannel(controlChannel, nil)
	if err != nil {
		return nil, seen, err
	}
	requestDrain := make(chan struct{})
	go func() { defer close(requestDrain); ssh.DiscardRequests(channelRequests) }()
	if err := json.NewEncoder(control).Encode(greeting(cfg.Self)); err != nil {
		_ = client.Close()
		<-requestDrain
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
		} else if refusal := buildRefusal(cfg.Self, answer.Identity); refusal != nil {
			err = refusal
		}
	}
	if err != nil {
		_ = client.Close()
		<-requestDrain
		return nil, seen, err
	}
	if handshakeCtx.Err() != nil {
		_ = client.Close()
		<-requestDrain
		return nil, seen, handshakeCtx.Err()
	}
	if !stopRaw() && handshakeCtx.Err() != nil {
		_ = client.Close()
		<-requestDrain
		return nil, seen, handshakeCtx.Err()
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		_ = client.Close()
		<-requestDrain
		return nil, seen, err
	}
	conn := newConn(ctx, client, control, decoder, answer.Identity, requestDrain)
	success = true
	return conn, seen, nil
}
