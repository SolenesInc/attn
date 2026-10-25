package bridge

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

const AttachChannel = "attach"

type AttachRequest struct {
	Terminal   harness.TerminalID
	Subscriber string
	Options    ptybackend.AttachOptions
}

type attachStream struct {
	channel     ssh.Channel
	events      chan ptybackend.OutputEvent
	done        chan struct{}
	closeOnce   sync.Once
	acknowledge chan struct{}
}

func (s *attachStream) Events() <-chan ptybackend.OutputEvent { return s.events }
func (s *attachStream) Close() error {
	s.closeOnce.Do(func() { close(s.done); _ = s.channel.Close() })
	return nil
}
func (s *attachStream) Acknowledge() {
	select {
	case s.acknowledge <- struct{}{}:
	case <-s.done:
	}
}

func (c *Conn) Attach(ctx context.Context, request AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	var info ptybackend.AttachInfo
	payload, err := json.Marshal(request)
	if err != nil {
		return info, nil, err
	}
	channel, requests, err := c.Open(ctx, AttachChannel, payload)
	if err != nil {
		return info, nil, err
	}
	go ssh.DiscardRequests(requests)
	stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
	dec := json.NewDecoder(channel)
	err = dec.Decode(&info)
	stop()
	if err != nil {
		_ = channel.Close()
		return info, nil, err
	}
	stream := &attachStream{channel: channel, events: make(chan ptybackend.OutputEvent), done: make(chan struct{}), acknowledge: make(chan struct{})}
	go func() {
		defer close(stream.events)
		defer stream.Close()
		enc := json.NewEncoder(channel)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return
			}
			var event ptybackend.OutputEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return
			}
			select {
			case stream.events <- event:
			case <-stream.done:
				return
			}
			select {
			case <-stream.acknowledge:
			case <-stream.done:
				return
			}
			if err := enc.Encode(len(raw)); err != nil {
				return
			}
			if event.Kind == ptybackend.OutputEventKindDesync {
				return
			}
		}
	}()
	return info, stream, nil
}

func (c *Conn) ServeAttach(parent context.Context, incoming ssh.NewChannel, open func(AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error)) (failure error) {
	var request AttachRequest
	if err := json.Unmarshal(incoming.ExtraData(), &request); err != nil {
		_ = incoming.Reject(ssh.Prohibited, err.Error())
		return err
	}
	info, stream, err := open(request)
	if err != nil {
		_ = incoming.Reject(ssh.Prohibited, err.Error())
		return err
	}
	defer stream.Close()
	channel, requests, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
	defer stop()
	c.outputOnce.Do(func() { c.output = newOutputCredit(c.done) })
	credit := c.output
	flow := &outputFlow{}
	defer func() { cancel(); credit.closeFlow(flow) }()
	if err := json.NewEncoder(channel).Encode(info); err != nil {
		return err
	}
	acknowledgmentsEnded := make(chan struct{})
	go func() {
		defer close(acknowledgmentsEnded)
		defer cancel()
		dec := json.NewDecoder(channel)
		for {
			var n int
			if err := dec.Decode(&n); err != nil {
				return
			}
			credit.returned(flow, n)
		}
	}()
	defer func() { _ = channel.Close(); <-acknowledgmentsEnded }()
	for {
		var event ptybackend.OutputEvent
		select {
		case next, ok := <-stream.Events():
			if !ok {
				return
			}
			event = next
		case <-ctx.Done():
			return
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if event.Kind != ptybackend.OutputEventKindDesync {
			if err := credit.reserve(ctx, flow, len(raw)); err != nil {
				if ctx.Err() != nil {
					return
				}
				failure = err
				event.Kind = ptybackend.OutputEventKindDesync
			}
		}
		if event.Kind == ptybackend.OutputEventKindDesync {
			if err := credit.reserve(ctx, flow, 0); err != nil {
				return
			}
			raw, _ = json.Marshal(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindDesync, Reason: "bridge_backpressure"})
			if err := credit.reserve(ctx, flow, len(raw)); err != nil {
				return
			}
		}
		if _, err := channel.Write(append(raw, '\n')); err != nil {
			return err
		}
		if event.Kind == ptybackend.OutputEventKindDesync {
			return
		}
	}
}
