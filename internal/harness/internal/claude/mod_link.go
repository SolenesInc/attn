package claude

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

type claudeConversation struct {
	host  harness.Host
	mod   *claudeMod
	facts harness.SessionFacts
}

func (h claudeHarness) Conversation(facts harness.SessionFacts) harness.Conversation {
	return claudeConversation{host: h.host, mod: h.mod, facts: facts}
}

func (c claudeConversation) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	return c.hand(ctx, in, false)
}

func (c claudeConversation) Compact(ctx context.Context, in harness.Input) harness.Custody {
	return c.hand(ctx, in, true)
}

func (c claudeConversation) CanCompact() error {
	if c.host.Sessions().ChannelUp(c.facts.Session) {
		return nil
	}
	return protocol.Refuse(protocol.RefusalWrongState, "this Claude session does not run with attn's Claude mod, so attn has no way to compact it; reload the session so it loads the mod ({attn agent reload %s}), or close without --compact", claudeShortSession(c.facts.Session))
}

func (c claudeConversation) hand(ctx context.Context, in harness.Input, compact bool) harness.Custody {
	mod, terminal := c.mod, c.facts.Terminal
	if !mod.reachable(terminal) || (in.PendingApproval && !in.AskedInTurn) {
		return harness.Custody{Outcome: harness.NotMine}
	}
	delivery := &claudeModDelivery{session: c.facts.Session, input: in, compact: compact, answer: make(chan harness.Custody, 1)}
	pausepoint.At(pausepoint.ClaudeModEnqueuing)
	if !mod.enqueue(terminal, delivery) {
		return harness.Custody{Reason: fmt.Sprintf("claude mod: terminal %s stopped polling before taking input %s, send it again", terminal, in.ID)}
	}
	tripwire := c.host.Clock().NewTimer(claudemod.AckTimer, claudeModAckWait)
	defer tripwire.Stop()
	select {
	case custody := <-delivery.answer:
		return custody
	case <-tripwire.C():
		mod.forget(terminal, delivery)
		return harness.Custody{Reason: fmt.Sprintf("claude mod: max_ack_wait=%s; no answer for input %s", claudeModAckWait, in.ID)}
	case <-ctx.Done():
		mod.forget(terminal, delivery)
		return harness.Custody{Reason: fmt.Sprintf("claude mod: input %s: %v", in.ID, ctx.Err())}
	}
}

func (m *claudeMod) enqueue(terminal harness.TerminalID, delivery *claudeModDelivery) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[terminal]
	if t == nil {
		return false
	}
	t.queued = append(t.queued, delivery)
	m.giveLocked(t)
	return true
}

func (m *claudeMod) forget(terminal harness.TerminalID, delivery *claudeModDelivery) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[terminal]
	if t == nil {
		return
	}
	delete(t.handed, delivery.input.ID)
	for i, queued := range t.queued {
		if queued == delivery {
			t.queued = append(t.queued[:i], t.queued[i+1:]...)
			return
		}
	}
}
