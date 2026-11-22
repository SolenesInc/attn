package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
)

type serverCompaction struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

func (h sigmaHarness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if l := h.d.serverLinkFor(facts.Harness); l != nil && facts.Lifetime == harness.InServer {
		return l.conversation(facts)
	}
	return typedConversation{refusal: harness.NoCompaction(h.Facts().ID)}
}

func (s serverConversation) compact(ctx context.Context, in harness.Input) harness.Custody {
	l, session := s.link, s.session
	conversation := l.d.serverConversation(session)
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("session %s holds no %s conversation", session, l.b.name)}
	}
	if monitor := l.d.sessionMonitor(session); monitor == nil || !turnHasEnded(monitor) {
		return harness.Custody{Reason: fmt.Sprintf("busy: %s conversation %s is running", l.b.name, conversation)}
	}
	c, err := l.ensureServer(ctx, nil)
	if err != nil {
		l.d.events().Compaction(session, serverCompaction{}.outcome(in.ID, l.displayName(), err))
		return harness.Custody{Outcome: harness.Taken, At: time.Now()}
	}
	params := map[string]any{"conversation": conversation, "author": l.author(harness.VoiceAttn)}
	started := l.d.life.Go(l.b.agent+"Compact", func() {
		callCtx, cancel := context.WithTimeout(l.d.life.Context(), compactOutcomeTripwire)
		defer cancel()
		var compaction serverCompaction
		err := c.Call(callCtx, "conversation/compact", params, &compaction)
		l.d.events().Compaction(session, compaction.outcome(in.ID, l.displayName(), err))
	})
	if !started {
		return harness.Custody{Reason: "the daemon is stopping"}
	}
	return harness.Custody{Outcome: harness.Taken, At: time.Now()}
}

func (c serverCompaction) outcome(input, name string, err error) harness.Compaction {
	switch {
	case err != nil:
		return harness.Compaction{Input: input, Result: harness.CompactionFailed, Reason: fmt.Sprintf("conversation/compact: %v", err), TurnUnknown: true}
	case c.Outcome == "compacted", c.Outcome == "nothing_to_compact":
		return harness.Compaction{Input: input, Result: harness.Compacted}
	case c.Outcome == "stopped":
		return harness.Compaction{Input: input, Result: harness.CompactionFailed, Reason: name + " stopped the compaction before it ended"}
	case c.Message != "":
		return harness.Compaction{Input: input, Result: harness.CompactionFailed, Reason: c.Message}
	}
	return harness.Compaction{Input: input, Result: harness.CompactionFailed, Reason: fmt.Sprintf("%s answered the compaction %q", name, c.Outcome)}
}
