package daemon

import (
	"slices"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

type terminalSizes struct {
	mu         sync.Mutex
	byTerminal map[protocol.TerminalID]*sizeClaims
}

type sizeClaims struct {
	owner    *wsClient
	requests []sizeRequest
}

type sizeRequest struct {
	client *wsClient
	size   protocol.PtyResizeMessage
}

type ownerSize struct {
	owner *wsClient
	size  protocol.PtyResizeMessage
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

func (s *terminalSizes) request(client *wsClient, size protocol.PtyResizeMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byTerminal == nil {
		s.byTerminal = make(map[protocol.TerminalID]*sizeClaims)
	}
	claims := s.byTerminal[size.ID]
	if claims == nil {
		claims = &sizeClaims{}
		s.byTerminal[size.ID] = claims
	}
	claims.mostRecent(sizeRequest{client: client, size: size})
	if claims.owner == nil {
		claims.owner = client
	}
	return claims.owner == client
}

func (s *terminalSizes) claim(client *wsClient, terminal protocol.TerminalID) (protocol.PtyResizeMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims := s.byTerminal[terminal]
	if claims == nil {
		return protocol.PtyResizeMessage{}, false
	}
	request, asked := claims.requestOf(client)
	if !asked {
		return protocol.PtyResizeMessage{}, false
	}
	claims.mostRecent(request)
	if claims.owner == client {
		return protocol.PtyResizeMessage{}, false
	}
	claims.owner = client
	return request.size, true
}

func (s *terminalSizes) release(client *wsClient, terminal protocol.TerminalID) (ownerSize, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseLocked(client, terminal)
}

func (s *terminalSizes) releaseAll(client *wsClient) []ownerSize {
	s.mu.Lock()
	defer s.mu.Unlock()
	var handedOn []ownerSize
	for terminal := range s.byTerminal {
		if next, ok := s.releaseLocked(client, terminal); ok {
			handedOn = append(handedOn, next)
		}
	}
	return handedOn
}

func (s *terminalSizes) releaseLocked(client *wsClient, terminal protocol.TerminalID) (ownerSize, bool) {
	claims := s.byTerminal[terminal]
	if claims == nil {
		return ownerSize{}, false
	}
	claims.forget(client)
	if len(claims.requests) == 0 {
		delete(s.byTerminal, terminal)
		return ownerSize{}, false
	}
	if claims.owner != client {
		return ownerSize{}, false
	}
	next := claims.requests[len(claims.requests)-1]
	claims.owner = next.client
	return ownerSize{owner: next.client, size: next.size}, true
}
