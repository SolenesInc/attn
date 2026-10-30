package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/ssh"
)

const InputChannel = "input"

var ErrUnanswered = errors.New("the bridge ended before the answer")

type inputRequest struct {
	ID      uint64          `json:"id"`
	Request json.RawMessage `json:"request"`
}

type inputAnswer struct {
	ID    uint64 `json:"id"`
	Error string `json:"error,omitempty"`
}

type inputSender struct {
	mu      sync.Mutex
	nextID  uint64
	queued  []inputRequest
	pending map[uint64]func(error)
	wake    chan struct{}
	ended   bool
}

func (c *Conn) Send(request any, answered func(error)) {
	payload, err := json.Marshal(request)
	if err != nil {
		answered(fmt.Errorf("bridge input: encode request: %w", err))
		return
	}
	c.inputOnce.Do(func() {
		c.input = &inputSender{pending: make(map[uint64]func(error)), wake: make(chan struct{}, 1)}
		go c.sendInput()
	})
	s := c.input
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		answered(ErrUnanswered)
		return
	}
	s.nextID++
	s.queued = append(s.queued, inputRequest{ID: s.nextID, Request: payload})
	s.pending[s.nextID] = answered
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (c *Conn) sendInput() {
	s := c.input
	defer s.finish()
	defer c.cancel()
	channel, requests, err := c.Open(c.ctx, InputChannel, nil)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	written := make(chan struct{})
	go func() {
		defer close(written)
		defer channel.Close()
		encoder := json.NewEncoder(channel)
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-s.wake:
			}
			s.mu.Lock()
			queued := s.queued
			s.queued = nil
			s.mu.Unlock()
			for _, frame := range queued {
				if err := encoder.Encode(frame); err != nil {
					return
				}
			}
		}
	}()
	defer func() {
		c.cancel()
		_ = channel.Close()
		<-written
	}()
	decoder := json.NewDecoder(channel)
	for {
		var answer inputAnswer
		if err := decoder.Decode(&answer); err != nil {
			return
		}
		s.mu.Lock()
		answered := s.pending[answer.ID]
		delete(s.pending, answer.ID)
		s.mu.Unlock()
		if answered != nil {
			var err error
			if answer.Error != "" {
				err = &CallRejected{Kind: InputChannel, Reason: answer.Error}
			}
			answered(err)
		}
	}
}

func (s *inputSender) finish() {
	s.mu.Lock()
	s.ended = true
	pending := s.pending
	s.pending = nil
	s.queued = nil
	s.mu.Unlock()
	for _, answered := range pending {
		answered(ErrUnanswered)
	}
}

func ServeInput(incoming ssh.NewChannel, run func(json.RawMessage, func(error))) {
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	defer channel.Close()
	decoder := json.NewDecoder(channel)
	encoder := json.NewEncoder(channel)
	var answers sync.Mutex
	for {
		var request inputRequest
		if err := decoder.Decode(&request); err != nil {
			return
		}
		run(request.Request, func(err error) {
			answer := inputAnswer{ID: request.ID}
			if err != nil {
				answer.Error = err.Error()
			}
			answers.Lock()
			defer answers.Unlock()
			_ = encoder.Encode(answer)
		})
	}
}
