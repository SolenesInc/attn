package appserver

import (
	"context"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

const serverForked = "sigma.forked"

func (p *serverConnection) followFork(c *effectrpc.Client, conversation string) {
	if p.judged[conversation] || p.holder(conversation) != "" {
		return
	}
	for _, l := range p.peers() {
		authors := l.b.authors.Load()
		if authors == nil {
			continue
		}
		if p.judged == nil {
			p.judged = make(map[string]bool)
		}
		p.judged[conversation] = true
		p.c.host.Life().Go("serverFork", func() { l.followFork(p.c.host.Life().Context(), c, p, authors, conversation) })
		return
	}
}

func (p *serverConnection) holder(conversation string) protocol.SessionID {
	for _, l := range p.peers() {
		if session := l.holder(conversation); session != "" {
			return session
		}
	}
	return ""
}

func (p *serverConnection) takeFork(conversation string) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, forked := p.forks[conversation]
	delete(p.forks, conversation)
	return entry, forked
}

func (l *Link) followFork(ctx context.Context, c *effectrpc.Client, p *serverConnection, authors *TerminalAuthors, conversation string) {
	terminal, forked, ok, retryable := l.forkedBy(ctx, c, authors, conversation)
	if retryable {
		p.events.run(p.c, func() { delete(p.judged, conversation) })
		return
	}
	if !ok {
		return
	}
	p.mu.Lock()
	if p.forks == nil {
		p.forks = make(map[string]int64)
	}
	p.forks[conversation] = forked
	p.mu.Unlock()
	if wait := l.c.host.Events().Moved(harness.FromTerminal(terminal), harness.Move{Conversation: conversation}); wait != nil {
		if err := wait(ctx); err != nil {
			p.mu.Lock()
			delete(p.forks, conversation)
			p.mu.Unlock()
			l.logf("terminal %s did not move to its fork %s: %v", terminal, conversation, err)
		}
	}
}

func (l *Link) forkedBy(ctx context.Context, c *effectrpc.Client, authors *TerminalAuthors, conversation string) (harness.TerminalID, int64, bool, bool) {
	var listed []struct {
		ID   string `json:"id"`
		Fork *struct {
			Of string `json:"of"`
			At int64  `json:"at"`
		} `json:"fork"`
	}
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	err := c.Call(callCtx, "conversation/list", nil, &listed)
	cancel()
	if err != nil {
		l.logf("reading whether conversation %s is a fork: %v", conversation, err)
		return "", 0, false, true
	}
	parentOf := make(map[string]string, len(listed))
	atOf := make(map[string]int64, len(listed))
	for _, each := range listed {
		if each.Fork != nil && each.Fork.Of != "" {
			parentOf[each.ID] = each.Fork.Of
			atOf[each.ID] = each.Fork.At
		}
	}
	of := parentOf[conversation]
	if of == "" {
		return "", 0, false, false
	}
	holder := protocol.SessionID("")
	visited := map[string]bool{conversation: true}
	for of != "" && !visited[of] {
		visited[of] = true
		if holder = l.holder(of); holder != "" {
			break
		}
		of = parentOf[of]
	}
	if holder == "" {
		return "", 0, false, false
	}
	var page struct {
		Entries []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
			Data struct {
				Author struct {
					ID string `json:"id"`
				} `json:"author"`
			} `json:"data"`
		} `json:"entries"`
	}
	callCtx, cancel = context.WithTimeout(ctx, serverCallLimit)
	err = c.Call(callCtx, "conversation/entries", map[string]any{"conversation": conversation, "from": atOf[conversation] + 1, "order": "ascending", "limit": 1}, &page)
	cancel()
	if err != nil {
		l.logf("reading who forked conversation %s: %v", conversation, err)
		return "", 0, false, true
	}
	if len(page.Entries) == 0 || page.Entries[0].Kind != serverForked {
		return "", 0, false, false
	}
	terminal, ok := authors.Terminal(page.Entries[0].Data.Author.ID)
	if !ok {
		return "", 0, false, false
	}
	shown, showing := l.c.host.Sessions().Showing(terminal)
	if !showing || shown != holder {
		return "", 0, false, false
	}
	return terminal, page.Entries[0].ID, true, false
}
