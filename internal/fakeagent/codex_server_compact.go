package fakeagent

import "github.com/google/uuid"

const (
	methodCompactions   = "compactions"
	methodRefuseCompact = "refuse_compact"
	heldCompaction      = "compaction"
)

type compactionsResult struct {
	Count int `json:"count"`
}

func (s *CodexServer) HoldNextCompaction() (arrived, release func()) {
	s.t.Helper()
	arrived, answer := s.holdNextReply(heldCompaction)
	return arrived, func() { answer("") }
}

func (s *CodexServer) RefuseNextCompaction(reason string) {
	s.t.Helper()
	s.call(methodRefuseCompact, serverThreadParams{Text: reason}, nil)
}

func (s *CodexServer) Compactions(conversation string) int {
	s.t.Helper()
	var counted compactionsResult
	s.call(methodCompactions, serverThreadParams{ThreadID: conversation}, &counted)
	return counted.Count
}

func (s *codexAppServer) compactStart(p codexServerParams) (any, func(), error) {
	t, err := s.loaded(p.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	refusal := s.compactRefusal
	s.compactRefusal = ""
	s.mu.Unlock()
	if refusal != "" {
		return nil, nil, codexRefusal(refusal)
	}
	return map[string]any{}, func() { go s.compact(t) }, nil
}

func (s *codexAppServer) compact(t *codexServerThread) {
	s.mu.Lock()
	running := t.active
	s.mu.Unlock()
	if running {
		if _, after, err := s.endTurn(methodFakeHalt, codexServerParams{ThreadID: t.c.conversation}); err == nil {
			after()
		}
	}
	ended := make(chan struct{})
	s.mu.Lock()
	t.active = true
	t.compactEnded = ended
	t.c.turnID = uuid.NewString()
	turnID := t.c.turnID
	s.mu.Unlock()
	s.status(t)
	s.awaitHeldReply(heldCompaction)
	s.mu.Lock()
	if archivedDuringCompact := s.threads[t.c.conversation] != t; archivedDuringCompact {
		t.compactEnded = nil
		s.mu.Unlock()
		close(ended)
		s.stopWhenDrained()
		return
	}
	s.compactions[t.c.conversation]++
	s.mu.Unlock()
	s.tell(t, notification("item/completed", map[string]any{"threadId": t.c.conversation, "turnId": turnID, "item": map[string]any{"type": "contextCompaction", "id": uuid.NewString()}}))
	s.mu.Lock()
	t.active = false
	s.mu.Unlock()
	s.status(t)
	s.tell(t, notification("turn/completed", map[string]any{"threadId": t.c.conversation, "turn": map[string]any{"id": turnID, "status": "completed"}}))
	s.mu.Lock()
	t.compactEnded = nil
	s.mu.Unlock()
	close(ended)
	s.stopWhenDrained()
}

func (s *codexAppServer) awaitCompactEndForTypedTurn(conn *codexServerConn, t *codexServerThread) {
	s.mu.Lock()
	ended := t.compactEnded
	typed := conn.terminal
	s.mu.Unlock()
	if typed && ended != nil {
		<-ended
	}
}
