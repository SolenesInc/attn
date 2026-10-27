package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/victorarias/attn/internal/protocol"
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

func (c *Conn) Call(ctx context.Context, kind string, request, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("bridge call %s: encode request: %w", kind, err)
	}
	large := len(payload) > MaxCallRequest
	header := payload
	if large {
		route := map[string]string{}
		if placed, ok := request.(protocol.Placed); ok {
			route["cmd"] = placed.CommandName()
			if field := placed.Placement().Field; field != "" {
				route[field] = placed.Locate()
			}
		}
		header, _ = json.Marshal(callBodyHeader{Body: true, Route: route})
	}

	channel, requests, err := c.Open(ctx, kind, header)
	if err != nil {
		return err
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
	defer stop()
	if large {
		if _, err = channel.Write(append(payload, '\n')); err != nil {
			return err
		}
	}
	var reply callReply
	if err = json.NewDecoder(channel).Decode(&reply); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("bridge call %s: read response: %w", kind, err)
	}
	if reply.Error != "" {
		return &CallRejected{Kind: kind, Reason: reply.Error}
	}
	if response != nil {
		return json.Unmarshal(reply.Response, response)
	}
	return nil
}

type callBodyHeader struct {
	Body  bool              `json:"bridge_body"`
	Route map[string]string `json:"route"`
}

type callReply struct {
	Response json.RawMessage `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func CallHeader(incoming ssh.NewChannel) []byte {
	var header callBodyHeader
	if json.Unmarshal(incoming.ExtraData(), &header) == nil && header.Body {
		route, _ := json.Marshal(header.Route)
		return route
	}
	return incoming.ExtraData()
}

func Answer(incoming ssh.NewChannel, handle func(context.Context, []byte) (any, error)) {
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := json.RawMessage(incoming.ExtraData())
	var header callBodyHeader
	if json.Unmarshal(request, &header) == nil && header.Body {
		if err = json.NewDecoder(channel).Decode(&request); err != nil {
			return
		}
	}
	readEnded := make(chan struct{})
	go func() { defer close(readEnded); _, _ = io.Copy(io.Discard, channel); cancel() }()
	response, err := handle(ctx, request)
	reply := callReply{}
	if err != nil {
		reply.Error = err.Error()
	} else {
		reply.Response, err = json.Marshal(response)
		if err != nil {
			reply.Error = err.Error()
		}
	}
	_ = json.NewEncoder(channel).Encode(reply)
	_ = channel.CloseWrite()
	_ = channel.Close()
	<-readEnded
}
