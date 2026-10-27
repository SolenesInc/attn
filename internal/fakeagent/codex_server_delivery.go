package fakeagent

import "sync"

const (
	methodHoldTurnReply    = "hold_turn_reply"
	methodAwaitTurnReply   = "await_turn_reply"
	methodReleaseTurnReply = "release_turn_reply"
)

type codexTurnReply struct {
	arrived chan struct{}
	release chan struct{}
	taken   bool
}

func (s *CodexServer) HoldNextTurnReply() (arrived, release func()) {
	s.t.Helper()
	s.call(methodHoldTurnReply, struct{}{}, nil)
	var once sync.Once
	release = func() {
		s.t.Helper()
		once.Do(func() { s.call(methodReleaseTurnReply, struct{}{}, nil) })
	}
	s.t.Cleanup(release)
	return func() {
		s.t.Helper()
		s.call(methodAwaitTurnReply, struct{}{}, nil)
	}, release
}

func (s *codexAppServer) awaitTurnReply() {
	s.mu.Lock()
	held := s.turnReply
	if held != nil && !held.taken {
		held.taken = true
	} else {
		held = nil
	}
	s.mu.Unlock()
	if held != nil {
		close(held.arrived)
		<-held.release
	}
}
