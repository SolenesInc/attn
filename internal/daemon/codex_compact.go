package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type codexCompaction struct {
	session protocol.SessionID
	active  bool
}

func (l codexMonitorLink) Compact(_ context.Context, _ harness.Input) harness.Custody {
	r, target := l.shared, l.target
	conversation := target.Conversation.NativeID
	failed := func(err error) harness.Custody {
		r.d.compactions.report(r.d, target.Session, compactOutcome{failed: err.Error()})
		return harness.Custody{Taken: true, At: time.Now()}
	}
	if conversation == "" {
		return failed(fmt.Errorf("the shared Codex session has no conversation yet"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	s, client, err := r.control(ctx, target.Profile)
	if err != nil {
		return failed(err)
	}
	busy := false
	err = r.withResumed(ctx, client, target.Session, conversation, func(call codexCall, status codexThreadStatus) error {
		if status.Type == "active" {
			busy = true
			return nil
		}
		s.armCompaction(conversation, target.Session)
		if err := call("thread/compact/start", map[string]any{"threadId": conversation}, nil); err != nil {
			s.disarmCompaction(conversation)
			r.d.compactions.report(r.d, target.Session, compactOutcome{failed: fmt.Sprintf("thread/compact/start: %v", err)})
		}
		return nil
	})
	switch {
	case err != nil:
		return failed(err)
	case busy:
		return harness.Custody{Reason: fmt.Sprintf("busy: shared Codex conversation %s is running a turn", conversation)}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func (s *codexServer) armCompaction(conversation string, session protocol.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.compacting == nil {
		s.compacting = make(map[string]*codexCompaction)
	}
	s.compacting[conversation] = &codexCompaction{session: session}
}

func (s *codexServer) disarmCompaction(conversation string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.compacting, conversation)
}

func (s *codexServer) compactionStepLocked(conversation, status string) (protocol.SessionID, bool) {
	compaction := s.compacting[conversation]
	switch {
	case compaction == nil:
		return "", false
	case status == "active":
		compaction.active = true
		return "", false
	case status == "idle" && compaction.active:
		delete(s.compacting, conversation)
		return compaction.session, true
	}
	return "", false
}
