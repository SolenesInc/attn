package appserver

import (
	"context"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

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

type serverShown struct {
	Author       serverAuthor `json:"author"`
	Conversation string       `json:"conversation"`
}

type serverAuthor struct {
	ID string `json:"id"`
}

func (p *serverConnection) followShown(shown []serverShown) {
	newest := make(map[string]string, len(shown))
	var authors []string
	for _, each := range shown {
		if _, seen := newest[each.Author.ID]; !seen {
			authors = append(authors, each.Author.ID)
		}
		newest[each.Author.ID] = each.Conversation
	}
	for _, author := range authors {
		p.followTerminalOf(author, newest[author])
	}
}

func (p *serverConnection) followTerminalOf(author, conversation string) {
	peers := p.peers()
	for _, l := range peers {
		authors := l.b.authors.Load()
		if authors == nil {
			continue
		}
		terminal, ok := authors.Terminal(author)
		if !ok {
			continue
		}
		session, showing := p.c.host.Sessions().Showing(terminal)
		if !showing || p.c.host.Sessions().Conversation(session) == conversation || p.holder(conversation) != "" {
			return
		}
		for _, owner := range peers {
			if owner.runsHere(session) {
				owner.move(terminal, conversation)
				return
			}
		}
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

func (l *Link) move(terminal harness.TerminalID, conversation string) {
	wait := l.c.host.Events().Moved(harness.FromTerminal(terminal), harness.Move{Conversation: conversation})
	if wait == nil {
		return
	}
	l.c.host.Life().Go("serverShown", func() {
		if err := wait(l.c.host.Life().Context()); err != nil {
			l.logf("terminal %s did not move to conversation %s it shows: %v", terminal, conversation, err)
		}
	})
}

func (l *Link) listConversations(ctx context.Context, c *effectrpc.Client) ([]listedConversation, error) {
	var listed []listedConversation
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	return listed, c.Call(callCtx, "conversation/list", nil, &listed)
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
