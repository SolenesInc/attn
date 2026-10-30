package fakeagent

import "sync"

const (
	methodHoldReply    = "hold_reply"
	methodAwaitReply   = "await_reply"
	methodReleaseReply = "release_reply"

	heldTurnReply   = "turn"
	heldInjectReply = "inject"
)

type codexHeldReply struct {
	arrived chan struct{}
	release chan struct{}
	taken   bool
	refusal string
}

type heldReplyParams struct {
	Reply   string `json:"reply"`
	Refusal string `json:"refusal"`
}

func (s *CodexServer) HoldNextTurnReply() (arrived, release func()) {
	s.t.Helper()
	arrived, answer := s.holdNextReply(heldTurnReply)
	return arrived, func() { answer("") }
}

// HoldNextInjectReply holds the reply to the next thread/inject_items after its items are written.
// Releasing with a refusal answers it with that error instead of success.
func (s *CodexServer) HoldNextInjectReply() (arrived func(), release func(refusal string)) {
	s.t.Helper()
	return s.holdNextReply(heldInjectReply)
}

func (s *CodexServer) holdNextReply(reply string) (arrived func(), release func(refusal string)) {
	s.t.Helper()
	s.call(methodHoldReply, heldReplyParams{Reply: reply}, nil)
	var once sync.Once
	release = func(refusal string) {
		s.t.Helper()
		once.Do(func() { s.call(methodReleaseReply, heldReplyParams{Reply: reply, Refusal: refusal}, nil) })
	}
	s.t.Cleanup(func() { release("") })
	return func() {
		s.t.Helper()
		s.call(methodAwaitReply, heldReplyParams{Reply: reply}, nil)
	}, release
}

func (s *codexAppServer) awaitHeldReply(reply string) (refusal string) {
	s.mu.Lock()
	held := s.held[reply]
	if held != nil && !held.taken {
		held.taken = true
	} else {
		held = nil
	}
	s.mu.Unlock()
	if held == nil {
		return ""
	}
	close(held.arrived)
	<-held.release
	return held.refusal
}

func (s *codexAppServer) handleHeldReply(method string, p heldReplyParams) {
	s.mu.Lock()
	switch method {
	case methodHoldReply:
		s.held[p.Reply] = &codexHeldReply{arrived: make(chan struct{}), release: make(chan struct{})}
		s.mu.Unlock()
	case methodAwaitReply:
		held := s.held[p.Reply]
		s.mu.Unlock()
		<-held.arrived
	case methodReleaseReply:
		held := s.held[p.Reply]
		delete(s.held, p.Reply)
		held.refusal = p.Refusal
		s.mu.Unlock()
		close(held.release)
	}
}
