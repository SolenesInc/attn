package ingest

import "sync"

type keyedMutex struct {
	mu   sync.Mutex
	held map[string]*keyLock
}

type keyLock struct {
	mu    sync.Mutex
	users int
}

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	entry, ok := k.held[key]
	if !ok {
		entry = &keyLock{}
		k.held[key] = entry
	}
	entry.users++
	k.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.users--
		if entry.users == 0 {
			delete(k.held, key)
		}
		k.mu.Unlock()
	}
}
