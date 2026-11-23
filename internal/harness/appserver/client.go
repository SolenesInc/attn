package appserver

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/setting"
)

const (
	pageSize       = 200
	compactTimeout = 10 * time.Minute
)

// Client holds attn's links to the app servers that keep sessions' conversations, one connection per server socket.
type Client struct {
	host        harness.Host
	mu          sync.Mutex
	links       map[string]*Link
	connections map[string]*serverConnection
}

func New(host harness.Host) *Client {
	return &Client{host: host, links: make(map[string]*Link), connections: make(map[string]*serverConnection)}
}

// Binding is one harness's server: its declaration, once known, and when it takes new launches.
type Binding struct {
	Agent       string
	Plugin      string
	DisplayName func() string
	Declaration *Declaration
	Enabled     func() bool
	// Authors, when set, gives each terminal's client an author id of its own: input attn sends in the user's voice acts
	// as the client of the session's terminal, and a fork that client makes moves its terminal there.
	Authors *TerminalAuthors
}

// TerminalAuthors maps a terminal to the author id its client acts as, and back.
type TerminalAuthors struct {
	Of       func(harness.TerminalID) string
	Terminal func(author string) (harness.TerminalID, bool)
}

// Bind links a harness to its server, or gives a restored link its declaration. claim runs under the client's lock before the link changes.
func (c *Client) Bind(b Binding, claim func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[b.Agent]
	if l != nil && l.b.plugin != b.Plugin {
		return fmt.Errorf("agent %q belongs to plugin %q", b.Agent, l.b.plugin)
	}
	if l != nil && b.Declaration == nil {
		return fmt.Errorf("agent %q requires its server binding", b.Agent)
	}
	if l != nil && l.b.declaration.Load() != nil && !reflect.DeepEqual(l.b.declaration.Load(), b.Declaration) {
		return fmt.Errorf("server binding for %q is already registered; restart the daemon to change it", b.Agent)
	}
	if b.Declaration == nil {
		return nil
	}
	if claim != nil {
		if err := claim(); err != nil {
			return err
		}
	}
	if l == nil {
		c.links[b.Agent] = newLink(c, newServerBinding(b))
		return nil
	}
	l.b.declaration.Store(b.Declaration)
	l.b.authors.Store(b.Authors)
	l.b.enabled, l.b.displayName = b.Enabled, b.DisplayName
	return nil
}

// Restore links a harness whose server outlived the plugin that declared it, until the plugin registers again.
func (c *Client) Restore(b Binding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.links[b.Agent] = newLink(c, newServerBinding(b))
}

func (c *Client) serverHolding(id harness.SessionID) *Link {
	agent, lifetime := c.host.Sessions().Lifetime(id)
	if lifetime != harness.InServer {
		return nil
	}
	return c.Link(string(agent))
}

// Link is the harness's link, or nil when it has none.
func (c *Client) Link(agent string) *Link {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.links[agent]
}

func (c *Client) Links() []*Link {
	c.mu.Lock()
	defer c.mu.Unlock()
	links := make([]*Link, 0, len(c.links))
	for _, l := range c.links {
		links = append(links, l)
	}
	sort.Slice(links, func(i, j int) bool { return links[i].b.agent < links[j].b.agent })
	return links
}

func (c *Client) PluginSessions(plugin string) int {
	count := 0
	for _, l := range c.Links() {
		if l.b.plugin == plugin {
			count += len(l.sessions())
		}
	}
	return count
}

// ForgetPlugin drops the links of an uninstalled plugin, letting go of each server no other harness uses.
func (c *Client) ForgetPlugin(plugin string) {
	var removed []*Link
	c.mu.Lock()
	for agent, l := range c.links {
		if l.b.plugin == plugin {
			removed = append(removed, l)
			delete(c.links, agent)
		}
	}
	c.mu.Unlock()
	for _, l := range removed {
		if p, err := l.connection(); err == nil {
			p.releaseUnlessUsed()
			p.reconcileAgentTools()
		}
	}
}

func (c *Client) SettingChanged(key string) {
	if key == setting.AgentToolsEnabled.Key {
		c.host.Life().Go("reconcileAgentTools", c.reconcileAgentTools)
	}
}

type appServerEvents struct {
	mu      sync.Mutex
	queue   []func()
	running bool
}

func (q *appServerEvents) run(c *Client, f func()) {
	q.mu.Lock()
	q.queue = append(q.queue, f)
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()
	drain := func() {
		for {
			q.mu.Lock()
			if len(q.queue) == 0 {
				q.running = false
				q.mu.Unlock()
				return
			}
			next := q.queue[0]
			q.queue = q.queue[1:]
			q.mu.Unlock()
			next()
		}
	}
	if !c.host.Life().Go("appServerEvents", drain) {
		q.mu.Lock()
		q.queue, q.running = nil, false
		q.mu.Unlock()
	}
}

func resolvedBinary(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func stopFailureDetail(errorType, message string) string {
	detail := strings.TrimSpace(errorType)
	if message = strings.TrimSpace(message); message != "" {
		detail += ": " + message
	}
	return detail
}
