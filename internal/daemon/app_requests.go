package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type appRequests[R any] struct {
	mu      sync.Mutex
	pending map[string]appPending[R]
}

type appPending[R any] struct {
	client *wsClient
	reply  chan R
}

func (a *appRequests[R]) ask(client *wsClient, timeout time.Duration, send func(requestID string) bool, unavailable, timedOut R) (R, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		var zero R
		return zero, err
	}
	requestID := "app-" + hex.EncodeToString(bytes)
	reply := make(chan R, 1)
	a.mu.Lock()
	if a.pending == nil {
		a.pending = make(map[string]appPending[R])
	}
	a.pending[requestID] = appPending[R]{client: client, reply: reply}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, requestID)
		a.mu.Unlock()
	}()

	if !send(requestID) {
		return unavailable, nil
	}
	select {
	case result := <-reply:
		return result, nil
	case <-time.After(timeout):
		return timedOut, nil
	}
}

func (a *appRequests[R]) answer(client *wsClient, requestID string, reply R) {
	a.mu.Lock()
	pending, ok := a.pending[requestID]
	a.mu.Unlock()
	if !ok || pending.client != client {
		return
	}
	select {
	case pending.reply <- reply:
	default:
	}
}

func (a *appRequests[R]) failFor(client *wsClient, disconnected R) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, pending := range a.pending {
		if pending.client != client {
			continue
		}
		select {
		case pending.reply <- disconnected:
		default:
		}
	}
}
