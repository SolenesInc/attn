package daemon

import (
	"slices"
	"sync"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

type terminalSizes struct {
	mu         sync.Mutex
	byTerminal map[protocol.TerminalID]*sizeClaims
}

type sizeClaims struct {
	applying           sync.Mutex
	applies            int
	owner              *wsClient
	requests           []sizeRequest
	claimedBeforeAsked *wsClient
}

type sizeRequest struct {
	client *wsClient
	size   protocol.PtyResizeMessage
}

type handoff struct {
	owner    *wsClient
	terminal protocol.TerminalID
}

func (c *sizeClaims) requestOf(client *wsClient) (sizeRequest, bool) {
	index := slices.IndexFunc(c.requests, func(r sizeRequest) bool { return r.client == client })
	if index < 0 {
		return sizeRequest{}, false
	}
	return c.requests[index], true
}

func (c *sizeClaims) forget(client *wsClient) {
	c.requests = slices.DeleteFunc(c.requests, func(r sizeRequest) bool { return r.client == client })
}

func (c *sizeClaims) mostRecent(request sizeRequest) {
	c.forget(request.client)
	c.requests = append(c.requests, request)
}

func (s *terminalSizes) claimsFor(terminal protocol.TerminalID) *sizeClaims {
	if s.byTerminal == nil {
		s.byTerminal = make(map[protocol.TerminalID]*sizeClaims)
	}
	claims := s.byTerminal[terminal]
	if claims == nil {
		claims = &sizeClaims{}
		s.byTerminal[terminal] = claims
	}
	return claims
}

func (s *terminalSizes) request(client *wsClient, size protocol.PtyResizeMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims := s.claimsFor(size.ID)
	claims.mostRecent(sizeRequest{client: client, size: size})
	if claims.owner == nil || claims.claimedBeforeAsked == client {
		claims.owner = client
		claims.claimedBeforeAsked = nil
	}
	return claims.owner == client
}

func (s *terminalSizes) claim(client *wsClient, terminal protocol.TerminalID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims := s.claimsFor(terminal)
	request, asked := claims.requestOf(client)
	if !asked {
		claims.claimedBeforeAsked = client
		return false
	}
	claims.claimedBeforeAsked = nil
	claims.mostRecent(request)
	if claims.owner == client {
		return false
	}
	claims.owner = client
	return true
}

func (s *terminalSizes) applyIfOwner(client *wsClient, terminal protocol.TerminalID, apply func(protocol.PtyResizeMessage)) bool {
	s.mu.Lock()
	claims := s.byTerminal[terminal]
	if claims == nil {
		s.mu.Unlock()
		return false
	}
	claims.applies++
	s.mu.Unlock()
	defer s.endApply(terminal, claims)
	claims.applying.Lock()
	defer claims.applying.Unlock()
	s.mu.Lock()
	request, asked := claims.requestOf(client)
	owns := asked && claims.owner == client
	s.mu.Unlock()
	if !owns {
		return false
	}
	pausepoint.At(pausepoint.PtySizeOwnerChecked)
	apply(request.size)
	return true
}

func (s *terminalSizes) endApply(terminal protocol.TerminalID, claims *sizeClaims) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims.applies--
	s.dropIfIdle(terminal, claims)
}

func (s *terminalSizes) dropIfIdle(terminal protocol.TerminalID, claims *sizeClaims) {
	if len(claims.requests) == 0 && claims.claimedBeforeAsked == nil && claims.applies == 0 {
		delete(s.byTerminal, terminal)
	}
}

func (s *terminalSizes) release(client *wsClient, terminal protocol.TerminalID) (handoff, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseLocked(client, terminal)
}

func (s *terminalSizes) releaseAll(client *wsClient) []handoff {
	s.mu.Lock()
	defer s.mu.Unlock()
	var handedOn []handoff
	for terminal := range s.byTerminal {
		if next, ok := s.releaseLocked(client, terminal); ok {
			handedOn = append(handedOn, next)
		}
	}
	return handedOn
}

func (s *terminalSizes) releaseLocked(client *wsClient, terminal protocol.TerminalID) (handoff, bool) {
	claims := s.byTerminal[terminal]
	if claims == nil {
		return handoff{}, false
	}
	claims.forget(client)
	if claims.claimedBeforeAsked == client {
		claims.claimedBeforeAsked = nil
	}
	defer s.dropIfIdle(terminal, claims)
	if claims.owner != client {
		return handoff{}, false
	}
	claims.owner = nil
	if len(claims.requests) == 0 {
		return handoff{}, false
	}
	next := claims.requests[len(claims.requests)-1]
	claims.owner = next.client
	return handoff{owner: next.client, terminal: terminal}, true
}
