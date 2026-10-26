package store

import (
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

// sessionRows caches reads of the sessions and session_runtime tables until the next write to either. Every write holds
// s.mu exclusively and cached reads hold it shared, so a row read under a generation stays current.
type sessionRows struct {
	mu      sync.Mutex
	gen     uint64
	rows    map[protocol.SessionID]sessionRow
	drivers map[protocol.SessionID]AgentDriverReportCursor
}

type sessionRow struct {
	session *protocol.Session
	copied  bool
}

func (c *sessionRows) resetLocked(gen uint64) {
	if c.gen == gen && c.rows != nil {
		return
	}
	c.gen = gen
	c.rows = make(map[protocol.SessionID]sessionRow)
	c.drivers = make(map[protocol.SessionID]AgentDriverReportCursor)
}

func (c *sessionRows) session(id protocol.SessionID, gen uint64) (*protocol.Session, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked(gen)
	row, ok := c.rows[id]
	if !ok {
		return nil, false, false
	}
	return cloneSession(row.session), row.copied, true
}

func (c *sessionRows) putSession(id protocol.SessionID, gen uint64, session *protocol.Session, copied bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked(gen)
	c.rows[id] = sessionRow{session: cloneSession(session), copied: copied}
}

func (c *sessionRows) driver(id protocol.SessionID, gen uint64) (AgentDriverReportCursor, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked(gen)
	cursor, ok := c.drivers[id]
	return cursor, ok
}

func (c *sessionRows) putDriver(id protocol.SessionID, gen uint64, cursor AgentDriverReportCursor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked(gen)
	c.drivers[id] = cursor
}

func (s *Store) sessionsGeneration() (uint64, bool) {
	if s.writes == nil {
		return 0, false
	}
	return s.writes.sessions.Load(), true
}
