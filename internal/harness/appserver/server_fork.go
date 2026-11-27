package appserver

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type conversationKind uint8

const (
	rootConversation conversationKind = iota
	workerConversation
	hostedSubagentConversation
	forkConversation
)

type listedConversation struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Fork  *struct {
		Of string `json:"of"`
		At int64  `json:"at"`
	} `json:"fork"`
	Lineage *struct {
		Parent string `json:"parent"`
		Root   string `json:"root"`
		Title  string `json:"title"`
		Task   string `json:"task"`
	} `json:"lineage"`
}

func (c listedConversation) kind() conversationKind {
	if c.Lineage != nil {
		if c.Lineage.Task != "" {
			return workerConversation
		}
		if c.Lineage.Root != "" && c.Lineage.Root != c.ID {
			return hostedSubagentConversation
		}
	}
	if c.Fork != nil && c.Fork.Of != "" {
		return forkConversation
	}
	return rootConversation
}

func (c listedConversation) workerTitle() string {
	if c.Lineage != nil && strings.TrimSpace(c.Lineage.Title) != "" {
		return strings.TrimSpace(c.Lineage.Title)
	}
	return strings.TrimSpace(c.Title)
}

type serverShown struct {
	Author       serverAuthor `json:"author"`
	Conversation string       `json:"conversation"`
}

type serverAuthor struct {
	ID string `json:"id"`
}

func (p *serverConnection) followShown(c *effectrpc.Client, shown []serverShown) {
	newest := make(map[string]string, len(shown))
	var authors []string
	for _, each := range shown {
		if _, seen := newest[each.Author.ID]; !seen {
			authors = append(authors, each.Author.ID)
		}
		newest[each.Author.ID] = each.Conversation
	}
	for _, author := range authors {
		p.followTerminalOf(c, author, newest[author])
	}
}

func (p *serverConnection) followTerminalOf(c *effectrpc.Client, author, conversation string) {
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
		held := p.c.host.Sessions().Conversation(session)
		if !showing || held == conversation || p.holder(conversation) != "" {
			return
		}
		for _, owner := range peers {
			if owner.runsHere(session) {
				owner.move(terminal, harness.Move{Conversation: conversation, Forked: owner.forksFrom(c, conversation, held)})
				return
			}
		}
		return
	}
}

func (p *serverConnection) holder(conversation string) protocol.SessionID {
	id, _ := p.holderWithLink(conversation)
	return id
}

func (p *serverConnection) holderWithLink(conversation string) (protocol.SessionID, *Link) {
	for _, l := range p.peers() {
		if session := l.holder(conversation); session != "" {
			return session, l
		}
	}
	return "", nil
}

func (l *Link) forksFrom(c *effectrpc.Client, conversation, held string) bool {
	if held == "" {
		return false
	}
	listed, ok, err := l.findListedConversation(l.c.host.Life().Context(), c, conversation)
	if err != nil {
		l.logf("conversation %s a terminal shows could not be listed, so its move offers no original: %v", conversation, err)
		return false
	}
	return ok && listed.kind() == forkConversation && listed.Fork.Of == held
}

func (l *Link) move(terminal harness.TerminalID, to harness.Move) {
	conversation := to.Conversation
	wait := l.c.host.Events().Moved(harness.FromTerminal(terminal), to)
	if wait == nil {
		return
	}
	l.c.host.Life().Go("serverShown", func() {
		if err := wait(l.c.host.Life().Context()); err != nil {
			l.logf("terminal %s did not move to conversation %s it shows: %v", terminal, conversation, err)
		}
	})
}

func (p *serverConnection) listConversations(ctx context.Context, c *effectrpc.Client) ([]listedConversation, error) {
	var listed []listedConversation
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	return listed, c.Call(callCtx, "conversation/list", nil, &listed)
}

func (l *Link) findListedConversation(ctx context.Context, c *effectrpc.Client, conversation string) (listedConversation, bool, error) {
	var listed []listedConversation
	callCtx, cancel := context.WithTimeout(ctx, serverCallLimit)
	defer cancel()
	if err := c.Call(callCtx, "conversation/list", nil, &listed); err != nil {
		return listedConversation{}, false, err
	}
	for _, each := range listed {
		if each.ID == conversation {
			return each, true, nil
		}
	}
	return listedConversation{}, false, nil
}
