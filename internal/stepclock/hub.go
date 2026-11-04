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
	firing   sync.Mutex
	settled  chan struct{}
}

type peerTimer struct {
	peer *peer
	id   string
}

type peer struct {
	conn       net.Conn
	pid        int
	ready      bool
	busy       bool
	unanswered map[string][]chan struct{}
	done       chan struct{}
	err        error
}

func Listen(socket string) (*Hub, error) {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	h := &Hub{socket: socket, listener: listener, pending: newPending[peerTimer](), accepted: make(chan struct{}), peers: make(map[*peer]struct{}), settled: make(chan struct{})}
	go func() {
		defer close(h.accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p := &peer{conn: conn, unanswered: make(map[string][]chan struct{}), done: make(chan struct{})}
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
		h.markSettledLocked()
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
	case len(fields) == 4 && fields[0] == "armed":
		d, err := time.ParseDuration(fields[3])
		if err != nil {
			return false
		}
		h.mu.Lock()
		h.pending.arm(peerTimer{p, fields[1]}, Name(fields[2]), d)
		h.mu.Unlock()
	case len(fields) == 2 && fields[0] == "disarmed":
		h.mu.Lock()
		h.pending.disarm(peerTimer{p, fields[1]})
		h.mu.Unlock()
	case len(fields) == 2 && (fields[0] == "advanced" || fields[0] == "fired"):
		return h.answer(p, fields[0])
	case len(fields) == 1 && (fields[0] == "busy" || fields[0] == "idle"):
		h.mu.Lock()
		p.busy = fields[0] == "busy"
		h.markSettledLocked()
		h.mu.Unlock()
	default:
		return false
	}
	return true
}

func (h *Hub) answer(p *peer, answer string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	oldest := p.unanswered[answer]
	if len(oldest) == 0 {
		return false
	}
	close(oldest[0])
	p.unanswered[answer] = oldest[1:]
	return true
}

// Advance moves every connected process's clock by d and returns once each has fired its due timers or exited.
func (h *Hub) Advance(ctx context.Context, d time.Duration) error {
	h.stepping.Lock()
	defer h.stepping.Unlock()
	h.mu.Lock()
	h.pending.mark()
	h.mu.Unlock()
	return h.command(ctx, fmt.Sprintf("advance %s", d), "advanced")
}

// Fire fires every timer named name in every connected process now, without moving the clocks, and returns once each
// has run their callbacks or exited. It is for a timer whose moment is no promise, such as a poll for changes the test
// has just made. It neither waits for nor moves the AwaitTimer mark, so it runs beside a pending AwaitTimer.
func (h *Hub) Fire(ctx context.Context, name Name) error {
	h.firing.Lock()
	defer h.firing.Unlock()
	return h.command(ctx, fmt.Sprintf("fire %s", name), "fired")
}

func (h *Hub) command(ctx context.Context, command, answer string) error {
	h.mu.Lock()
	peers := make([]*peer, 0, len(h.peers))
	answers := make(map[*peer]chan struct{}, len(h.peers))
	for p := range h.peers {
		if p.ready {
			peers = append(peers, p)
			answers[p] = make(chan struct{})
			p.unanswered[answer] = append(p.unanswered[answer], answers[p])
		}
	}
	h.mu.Unlock()
	for _, p := range peers {
		if _, err := fmt.Fprintln(p.conn, command); err != nil {
			select {
			case <-p.done:
				continue
			case <-ctx.Done():
				return fmt.Errorf("%s in process %d: %w", command, p.pid, err)
			}
		}
	}
	for _, p := range peers {
		select {
		case <-answers[p]:
		case <-p.done:
		case <-ctx.Done():
			return fmt.Errorf("process %d did not answer %s", p.pid, command)
		}
	}
	return nil
}

// AwaitTimer waits until any process has a timer named name pending, armed or reset after the last Advance or AwaitTimer.
// When ctx ends first it returns false with the timers pending then, as "name duration".
func (h *Hub) AwaitTimer(ctx context.Context, name Name) ([]string, bool) {
	h.stepping.Lock()
	defer h.stepping.Unlock()
	return h.pending.await(ctx, &h.mu, name)
}

// AwaitIdle waits until no connected process holds its clock busy, as the job runner does while it has work in hand.
// When ctx ends first it returns false with the pids still busy.
func (h *Hub) AwaitIdle(ctx context.Context) ([]int, bool) {
	for {
		h.mu.Lock()
		var busy []int
		for p := range h.peers {
			if p.busy {
				busy = append(busy, p.pid)
			}
		}
		settled := h.settled
		h.mu.Unlock()
		if len(busy) == 0 {
			return nil, true
		}
		select {
		case <-settled:
		case <-ctx.Done():
			return busy, false
		}
	}
}

func (h *Hub) markSettledLocked() {
	close(h.settled)
	h.settled = make(chan struct{})
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
