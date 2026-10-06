package daemon

import (
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

type spawnLock struct {
	mu   sync.Mutex
	refs int
}

func (d *Daemon) acquireSpawnLock(sessionID protocol.SessionID) (release func()) {
	d.spawnLocksMu.Lock()
	lock := d.spawnLocks[sessionID]
	if lock == nil {
		lock = &spawnLock{}
		d.spawnLocks[sessionID] = lock
	}
	lock.refs++
	d.spawnLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		d.spawnLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(d.spawnLocks, sessionID)
		}
		d.spawnLocksMu.Unlock()
	}
}
