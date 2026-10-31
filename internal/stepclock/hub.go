package stepclock

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hub steps the clocks of every process started with its Env, as one clock.
type Hub struct {
	socket   string
	listener net.Listener
	pending  pending[peerTimer]
	accepted chan struct{}
	workers  sync.WaitGroup
	mu       sync.Mutex
	peers    map[*peer]struct{}
	stepping sync.Mutex
}

type peerTimer struct {
	peer *peer
	id   string
}

type peer struct {
	conn     net.Conn
	pid      int
	ready    bool
	advanced chan struct{}
	done     chan struct{}
	err      error
}

func Listen(socket string) (*Hub, error) {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	h := &Hub{socket: socket, listener: listener, pending: newPending[peerTimer](), accepted: make(chan struct{}), peers: make(map[*peer]struct{})}
	go func() {
		defer close(h.accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p := &peer{conn: conn, advanced: make(chan struct{}, 1), done: make(chan struct{})}
			h.mu.Lock()
			h.peers[p] = struct{}{}
			h.mu.Unlock()
			h.workers.Go(func() { h.admit(p) })
		}
	}()
	return h, nil
}

func (h *Hub) Env() string { return EnvSocket + "=" + h.socket }

func (h *Hub) admit(p *peer) {
	defer func() {
		p.conn.Close()
		h.mu.Lock()
		delete(h.peers, p)
		for _, key := range h.pending.armed() {
			if key.peer == p {
				h.pending.disarm(key)
			}
		}
		h.mu.Unlock()
		close(p.done)
	}()
	reader := bufio.NewReader(p.conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		p.err = err
		return
	}
	command, pid, _ := strings.Cut(strings.TrimSpace(line), " ")
	if p.pid, p.err = strconv.Atoi(pid); command != "ready" || p.err != nil {
		p.err = fmt.Errorf("invalid stepped clock greeting %q", strings.TrimSpace(line))
		return
	}
	h.mu.Lock()
	p.ready = true
	h.mu.Unlock()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			p.err = err
			return
		}
		fields := strings.Fields(line)
		if !h.apply(p, fields) {
			p.err = fmt.Errorf("process %d sent %q", p.pid, strings.TrimSpace(line))
			return
		}
	}
}

func (h *Hub) apply(p *peer, fields []string) bool {
	switch {
	case len(fields) == 3 && fields[0] == "armed":
		d, err := time.ParseDuration(fields[2])
		if err != nil {
			return false
		}
		h.mu.Lock()
		h.pending.arm(peerTimer{p, fields[1]}, d)
		h.mu.Unlock()
	case len(fields) == 2 && fields[0] == "disarmed":
		h.mu.Lock()
		h.pending.disarm(peerTimer{p, fields[1]})
		h.mu.Unlock()
	case len(fields) == 2 && fields[0] == "advanced":
		p.advanced <- struct{}{}
	case len(fields) == 1 && (fields[0] == "busy" || fields[0] == "idle"):
	default:
		return false
	}
	return true
}

// Advance moves every connected process's clock by d and returns once each has fired its due timers or exited.
func (h *Hub) Advance(ctx context.Context, d time.Duration) error {
	h.stepping.Lock()
	defer h.stepping.Unlock()
	h.mu.Lock()
	h.pending.mark()
	peers := make([]*peer, 0, len(h.peers))
	for p := range h.peers {
		if p.ready {
			peers = append(peers, p)
		}
	}
	h.mu.Unlock()
	for _, p := range peers {
		if _, err := fmt.Fprintf(p.conn, "advance %s\n", d); err != nil {
			select {
			case <-p.done:
				continue
			case <-ctx.Done():
				return fmt.Errorf("advance process %d by %s: %w", p.pid, d, err)
			}
		}
	}
	for _, p := range peers {
		select {
		case <-p.advanced:
		case <-p.done:
		case <-ctx.Done():
			return fmt.Errorf("process %d did not advance by %s", p.pid, d)
		}
	}
	return nil
}

// AwaitTimer waits until any process has a timer of duration d pending, armed or reset after the last Advance or AwaitTimer.
// When ctx ends first it returns false with the durations of the timers pending then.
func (h *Hub) AwaitTimer(ctx context.Context, d time.Duration) ([]time.Duration, bool) {
	h.stepping.Lock()
	defer h.stepping.Unlock()
	return h.pending.await(ctx, &h.mu, d)
}

func (h *Hub) Close() {
	_ = h.listener.Close()
	<-h.accepted
	h.mu.Lock()
	for p := range h.peers {
		_ = p.conn.Close()
	}
	h.mu.Unlock()
	h.workers.Wait()
}
