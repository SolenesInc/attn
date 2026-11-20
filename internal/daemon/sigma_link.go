package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
)

type sigmaSessionLink struct{ serverSessionLink }

func (sigmaSessionLink) SafeDuringQuestion() {}

type sigmaCompaction struct {
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

func (s sigmaSessionLink) Compact(ctx context.Context, in harness.Input) harness.Custody {
	l, session := s.link, s.session
	conversation := l.d.linkConversation(session)
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("session %s holds no %s conversation", session, l.b.name)}
	}
	if monitor := l.d.sessionMonitor(session); monitor == nil || !turnHasEnded(monitor) {
		return harness.Custody{Reason: fmt.Sprintf("busy: %s conversation %s is running", l.b.name, conversation)}
	}
	c, err := l.ensureServer(ctx, nil)
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("compact %s conversation %s: %v", l.b.name, conversation, err)}
	}
	params := map[string]any{"conversation": conversation, "author": l.author(harness.VoiceAttn)}
	started := l.d.life.Go(l.b.agent+"Compact", func() {
		callCtx, cancel := context.WithTimeout(l.d.life.Context(), compactOutcomeTripwire)
		defer cancel()
		var compaction sigmaCompaction
		err := c.Call(callCtx, "conversation/compact", params, &compaction)
		l.d.compactions.report(l.d, session, in.ID, compaction.outcome(err))
	})
	if !started {
		return harness.Custody{Reason: "the daemon is stopping"}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func (c sigmaCompaction) outcome(err error) compactOutcome {
	switch {
	case err != nil:
		return compactOutcome{failed: fmt.Sprintf("conversation/compact: %v", err), turnUnknown: true}
	case c.Outcome == "compacted", c.Outcome == "nothing_to_compact":
		return compactOutcome{}
	case c.Outcome == "stopped":
		return compactOutcome{failed: "Sigma stopped the compaction before it ended"}
	case c.Message != "":
		return compactOutcome{failed: c.Message}
	}
	return compactOutcome{failed: fmt.Sprintf("Sigma answered the compaction %q", c.Outcome)}
}
