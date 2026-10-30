package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const controlChannel = "attn-control"

type ServerConfig struct {
	HostKeyPath string
	Self        Identity
	Admit       func(Identity) *Refusal
	Logf        func(string, ...any)
	OnError     func(error)
	Handlers    Handlers
	Update      *UpdateServer
}

func Serve(ctx context.Context, listener net.Listener, cfg ServerConfig, onConn func(*Conn)) error {
	defer listener.Close()
	if cfg.Update != nil {
		update := *cfg.Update
		restart := update.Restart
		update.Restart = func() {
			_ = listener.Close()
			restart()
		}
		cfg.Update = &update
	}
	key, err := hostKey(cfg.HostKeyPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopListener()
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		raw, err := listener.Accept()
		if err != nil {
			cancel()
			if ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		handlers.Go(func() {
			defer raw.Close()
			stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
			defer stop()
			if err := serveConn(ctx, raw, key, cfg, onConn); err != nil && ctx.Err() == nil && cfg.OnError != nil {
				cfg.OnError(err)
			}
		})
	}
}

func serveConn(ctx context.Context, raw net.Conn, key ssh.Signer, cfg ServerConfig, onConn func(*Conn)) error {
	activity := &updateActivityConn{Conn: raw}
	raw = activity
	if err := raw.SetDeadline(time.Now().Add(LostAfter)); err != nil {
		return err
	}
	sshConfig := &ssh.ServerConfig{NoClientAuth: true}
	sshConfig.AddHostKey(key)
	transport, channels, requests, err := ssh.NewServerConn(raw, sshConfig)
	if err != nil {
		return fmt.Errorf("bridge SSH authentication: %w", err)
	}
	defer transport.Wait()
	defer transport.Close()
	var drains sync.WaitGroup
	globalRequests := requests
	drains.Go(func() { ssh.DiscardRequests(globalRequests) })
	defer drains.Wait()
	defer transport.Close()
	var control ssh.Channel
	for incoming := range channels {
		if incoming.ChannelType() != controlChannel {
			_ = incoming.Reject(ssh.UnknownChannelType, "only attn-control is supported")
			continue
		}
		var channelRequests <-chan *ssh.Request
		control, channelRequests, err = incoming.Accept()
		if err != nil {
			return err
		}
		drains.Go(func() { ssh.DiscardRequests(channelRequests) })
		break
	}
	if control == nil {
		return fmt.Errorf("bridge closed before control channel")
	}
	decoder := json.NewDecoder(control)
	var home hello
	if err := decoder.Decode(&home); err != nil {
		return fmt.Errorf("read home hello: %w", err)
	}
	if err := home.check(); err != nil {
		return err
	}
	answer := greeting(cfg.Self)
	answer.Refused = instanceRefusal(home.Identity, cfg.Self)
	if answer.Refused == nil && cfg.Admit != nil {
		answer.Refused = cfg.Admit(home.Identity)
	}
	if home.Update == 1 && cfg.Update != nil {
		answer.Update, answer.Target = 1, &cfg.Update.Target
	}
	if answer.Refused == nil && answer.Update != 1 {
		answer.Refused = buildRefusal(cfg.Self, home.Identity)
	}
	if err := json.NewEncoder(control).Encode(answer); err != nil {
		return err
	}
	if answer.Refused != nil {
		return answer.Refused
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		return err
	}
	if answer.Update == 1 {
		return serveUpdate(ctx, transport, channels, home.Build, cfg.Update, activity)
	}
	conn := newConn(ctx, transport, channels, cfg.Handlers, control, decoder, home.Identity)
	onConn(conn)
	<-conn.Done()
	return nil
}
