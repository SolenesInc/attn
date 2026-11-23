package appserver

import (
	"context"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

const serverForked = "sigma.forked"

type listedConversation struct {
	ID   string `json:"id"`
	Fork *struct {
		Of string `json:"of"`
		At int64  `json:"at"`
	} `json:"fork"`
	Lineage *struct {
		Parent string `json:"parent"`
		Root   string `json:"root"`
		Task   string `json:"task"`
	} `json:"lineage"`
}

func (c listedConversation) child() bool {
	return c.Lineage != nil && c.Lineage.Root != "" && c.Lineage.Root != c.ID
}

func (c listedConversation) worker() bool {
	return c.child() && c.Lineage.Task != ""
}

// UnfollowedFork is the fork terminal t's program moved on to without attn following it, as after a /fork no prompt followed yet, or "".
func (c *Client) UnfollowedFork(ctx context.Context, t harness.TerminalID) (string, error) {
	session, showing := c.host.Sessions().Showing(t)
	if !showing {
		return "", nil
	}
	l := c.serverHolding(session)
	if l == nil || !l.runsHere(session) {
		return "", nil
	}
	authors := l.b.authors.Load()
	held := c.host.Sessions().Conversation(session)
	if authors == nil || held == "" {
		return "", nil
	}
	p, err := l.connection()
	if err != nil {
		return "", err
	}
	rpc, release, err := p.borrow(ctx, l, false)
	if err != nil {
		return "", err
	}
	defer release()
	return l.forkShownBy(ctx, rpc, authors.Of(t), held)
}

func (l *Link) forkShownBy(ctx context.Context, c *effectrpc.Client, author, held string) (string, error) {
	listed, err := l.listConversations(ctx, c)
	if err != nil {
		return "", err
	}
	forks := make(map[string][]listedConversation)
	for _, each := range listed {
		if each.Fork != nil && each.Fork.Of != "" && !each.child() {
			forks[each.Fork.Of] = append(forks[each.Fork.Of], each)
		}
	}
	shown := ""
	for from := held; ; {
		next := ""
		for _, fork := range forks[from] {
			if l.holder(fork.ID) != "" {
				continue
			}
			by, err := l.forkAuthor(ctx, c, fork.ID, fork.Fork.At)
			if err != nil {
				return "", err
			}
			if by == author {
				next = fork.ID
			}
		}
		if next == "" {
			return shown, nil
		}
		shown, from = next, next
	}
}

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

func (l *Link) followFork(ctx context.Context, c *effectrpc.Client, p *serverConnection, authors *TerminalAuthors, conversation string) {
	terminal, ok, retryable := l.forkedBy(ctx, c, authors, conversation)
	if retryable {
		p.events.run(p.c, func() { delete(p.judged, conversation) })
		return
	}
	if !ok {
		return
	}
	if err := l.move(ctx, terminal, conversation); err != nil {
		l.logf("terminal %s did not move to its fork %s: %v", terminal, conversation, err)
	}
}

func (l *Link) move(ctx context.Context, terminal harness.TerminalID, fork string) error {
	if wait := l.c.host.Events().Moved(harness.FromTerminal(terminal), harness.Move{Conversation: fork}); wait != nil {
		return wait(ctx)
	}
	return nil
}

func (l *Link) forkedBy(ctx context.Context, c *effectrpc.Client, authors *TerminalAuthors, conversation string) (harness.TerminalID, bool, bool) {
	listed, err := l.listConversations(ctx, c)
	if err != nil {
		l.logf("reading whether conversation %s is a fork: %v", conversation, err)
		return "", false, true
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
		return "", false, false
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
		return "", false, false
	}
	by, err := l.forkAuthor(ctx, c, conversation, atOf[conversation])
	if err != nil {
		l.logf("reading who forked conversation %s: %v", conversation, err)
		return "", false, true
	}
	terminal, ok := authors.Terminal(by)
	if !ok {
		return "", false, false
	}
	shown, showing := l.c.host.Sessions().Showing(terminal)
	if !showing || shown != holder {
		return "", false, false
	}
	return terminal, true, false
}

func (l *Link) listConversations(ctx context.Context, c *effectrpc.Client) ([]listedConversation, error) {
	var listed []listedConversation
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	return listed, c.Call(callCtx, "conversation/list", nil, &listed)
}

func (l *Link) forkAuthor(ctx context.Context, c *effectrpc.Client, conversation string, at int64) (string, error) {
	var page struct {
		Entries []struct {
			Kind string `json:"kind"`
			Data struct {
				Author struct {
					ID string `json:"id"`
				} `json:"author"`
			} `json:"data"`
		} `json:"entries"`
	}
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	if err := c.Call(callCtx, "conversation/entries", map[string]any{"conversation": conversation, "from": at + 1, "order": "ascending", "limit": 1}, &page); err != nil {
		return "", err
	}
	if len(page.Entries) == 0 || page.Entries[0].Kind != serverForked {
		return "", nil
	}
	return page.Entries[0].Data.Author.ID, nil
}

func (l *Link) forkedAt(ctx context.Context, c *effectrpc.Client, conversation string) (int64, error) {
	listed, err := l.listConversations(ctx, c)
	if err != nil {
		return 0, err
	}
	for _, each := range listed {
		if each.ID == conversation && each.Fork != nil {
			return each.Fork.At, nil
		}
	}
	return 0, nil
}
