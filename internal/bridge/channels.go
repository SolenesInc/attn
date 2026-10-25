package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// MaxCallRequest is the largest request a call carries in its channel-open payload. x/crypto v0.58.0
// carries at most 262118 bytes with a 4-byte kind (its packet limit is 256 KiB); this leaves room for the kind.
const MaxCallRequest = 255 << 10

// Handlers run on the bridge's one dispatch loop, in the order the peer opened the channels, so a
// handler that queues work before returning keeps that order. A handler must not block.
type Handlers map[string]func(*Conn, ssh.NewChannel)

type CallRejected struct {
	Kind   string
	Reason string
}

func (e *CallRejected) Error() string {
	return fmt.Sprintf("bridge call %s rejected: %s", e.Kind, e.Reason)
}

func (c *Conn) dispatch(channels <-chan ssh.NewChannel, handlers Handlers) {
	for incoming := range channels {
		if handle := handlers[incoming.ChannelType()]; handle != nil {
			handle(c, incoming)
			continue
		}
		_ = incoming.Reject(ssh.UnknownChannelType, fmt.Sprintf("daemon %s has no bridge handler for %q", c.peer.DaemonID, incoming.ChannelType()))
	}
}

func (c *Conn) Open(ctx context.Context, kind string, payload []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	type opened struct {
		channel  ssh.Channel
		requests <-chan *ssh.Request
		err      error
	}
	result := make(chan opened, 1)
	go func() {
		channel, requests, err := c.transport.OpenChannel(kind, payload)
		result <- opened{channel, requests, err}
	}()
	select {
	case r := <-result:
		var rejected *ssh.OpenChannelError
		if errors.As(r.err, &rejected) {
			return nil, nil, &CallRejected{Kind: kind, Reason: rejected.Message}
		}
		return r.channel, r.requests, r.err
	case <-ctx.Done():
		go func() {
			if r := <-result; r.err == nil {
				_ = r.channel.Close()
				ssh.DiscardRequests(r.requests)
			}
		}()
		return nil, nil, ctx.Err()
	}
}

// Call sends request in the channel-open payload and decodes the peer's response into response.
func (c *Conn) Call(ctx context.Context, kind string, request, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("bridge call %s: encode request: %w", kind, err)
	}
	if len(payload) > MaxCallRequest {
		return fmt.Errorf("bridge call %s: request is %d bytes, over the %d-byte limit (bridge.MaxCallRequest)", kind, len(payload), MaxCallRequest)
	}
	channel, requests, err := c.Open(ctx, kind, payload)
	if err != nil {
		return err
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
	defer stop()
	if response == nil {
		_, err = io.Copy(io.Discard, channel)
	} else {
		err = json.NewDecoder(channel).Decode(response)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("bridge call %s: read response: %w", kind, err)
	}
	return nil
}

// Answer runs handle on a call's request, then replies with its response, or rejects the call with
// its error. It blocks until the reply is sent, so run it off the dispatch loop.
func Answer(incoming ssh.NewChannel, handle func(request []byte) (any, error)) {
	response, err := handle(incoming.ExtraData())
	if err != nil {
		_ = incoming.Reject(ssh.Prohibited, err.Error())
		return
	}
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()
	_ = json.NewEncoder(channel).Encode(response)
	_ = channel.CloseWrite()
}
