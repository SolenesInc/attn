package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) OwnsCredential(subject string) bool {
	return c.serverConnectionAt(subject) != nil
}

func (c *Client) Authenticate(subject string, header http.Header) (harness.SessionID, string, bool, error) {
	p := c.serverConnectionAt(subject)
	if p == nil {
		return "", "", false, fmt.Errorf("attn holds no server at %s", subject)
	}
	name := p.callerConversationHeader()
	if name == "" {
		return "", "", false, fmt.Errorf("no harness of the server at %s names its conversations", subject)
	}
	conversation := strings.TrimSpace(header.Get(name))
	if conversation == "" {
		return "", "", false, fmt.Errorf("the request from the server at %s has no %s header", subject, name)
	}
	session := p.toolsCaller(conversation)
	if session == "" {
		session = p.adoptForCaller(conversation)
	}
	if session == "" {
		return "", "", false, fmt.Errorf("no session of %s holds conversation %s", p.harnesses(), conversation)
	}
	return session, p.harnesses() + " conversation " + conversation, false, nil
}

func (*Client) IdentifyCall(string, json.RawMessage) (harness.SessionID, string, *harness.Refusal) {
	return "", "", &harness.Refusal{Code: "no_session", Message: "the server's calls name their session at authentication"}
}

func (c *Client) declares(variable string) bool {
	if variable == "" {
		return false
	}
	for _, l := range c.Links() {
		if l.callerConversationEnv() == variable {
			return true
		}
	}
	return false
}

func (c *Client) callerOwner(variable, conversation string) (protocol.SessionID, bool) {
	var owner protocol.SessionID
	for _, id := range c.host.Sessions().Holding(conversation) {
		if l := c.serverHolding(id); l != nil && l.runsHere(id) && l.callerConversationEnv() == variable {
			if owner != "" {
				return "", true
			}
			owner = id
		}
	}
	return owner, false
}

func (c *Client) adoptForCallerEnv(variable, conversation string) {
	c.mu.Lock()
	connections := make([]*serverConnection, 0, len(c.connections))
	for _, p := range c.connections {
		connections = append(connections, p)
	}
	c.mu.Unlock()
	for _, p := range connections {
		for _, peer := range p.peers() {
			if peer.callerConversationEnv() == variable {
				if p.adoptForCaller(conversation) != "" {
					return
				}
				break
			}
		}
	}
}

func (c *Client) ResolveCaller(caller string) (harness.SessionID, harness.TerminalID, string, bool) {
	if named, ok := harness.ParseServerCaller(harness.TerminalID(caller)); ok {
		if named.Scope != "" || !c.declares(named.Variable) {
			return "", "", "", false
		}
		variable, conversation := named.Variable, named.Conversation
		owner, ambiguous := c.callerOwner(variable, conversation)
		if owner == "" && !ambiguous {
			c.adoptForCallerEnv(variable, conversation)
			owner, ambiguous = c.callerOwner(variable, conversation)
		}
		if ambiguous {
			return "", "", "", true
		}
		return owner, c.host.Sessions().PrimaryTerminal(owner), "", true
	}
	for _, l := range c.Links() {
		if session, view, ours := l.resolveCaller(protocol.TerminalID(caller)); ours {
			return session, view, "", true
		}
	}
	return "", "", "", false
}
