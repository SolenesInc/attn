package github

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{clients: make(map[string]*Client)}
}

func (r *ClientRegistry) Ensure(stopPacing context.Context, logf func(format string, args ...any), host, apiURL, token string) error {
	apiURL = resolveAPIURL(host, apiURL)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.clients[host]; ok && existing.baseURL == apiURL && existing.token == token {
		return nil
	}
	client, err := NewClientForHost(stopPacing, logf, host, apiURL, token)
	if err != nil {
		return err
	}
	r.clients[host] = client
	return nil
}

func (r *ClientRegistry) Remove(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, host)
}

func (r *ClientRegistry) Get(host string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[host]
	return client, ok
}

func (r *ClientRegistry) Hosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.clients) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(r.clients))
	for host := range r.clients {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func (r *ClientRegistry) FetchAllPRs() ([]*protocol.PR, error) {
	clients := r.snapshotClients()
	if len(clients) == 0 {
		return nil, nil
	}

	var allPRs []*protocol.PR
	var errOut error
	for host, client := range clients {
		prs, err := client.FetchAll()
		if err != nil {
			if errOut == nil {
				errOut = fmt.Errorf("fetch PRs for %s: %w", host, err)
			}
			continue
		}
		allPRs = append(allPRs, prs...)
	}
	return allPRs, errOut
}

func NewClientForHost(stopPacing context.Context, logf func(format string, args ...any), host, apiURL, token string) (*Client, error) {
	if token == "" {
		return nil, ErrNoToken
	}
	return newClientWithToken(stopPacing, logf, host, resolveAPIURL(host, apiURL), token)
}

func resolveAPIURL(host, apiURL string) string {
	if apiURL == "" {
		return mapHostToAPIURL(host)
	}
	return apiURL
}

func (r *ClientRegistry) snapshotClients() map[string]*Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.clients) == 0 {
		return nil
	}
	clients := make(map[string]*Client, len(r.clients))
	for host, client := range r.clients {
		clients[host] = client
	}
	return clients
}
