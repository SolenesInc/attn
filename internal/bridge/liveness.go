package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"golang.org/x/crypto/ssh"
)

const (
	HeartbeatInterval = 500 * time.Millisecond
	StallAfter        = 3 * time.Second
	LostAfter         = 10 * time.Second
	ReconnectInterval = 2 * time.Second
)

var errNoFrames = fmt.Errorf("no frame for %s", LostAfter)

type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateStalled    State = "stalled"
	StateLost       State = "lost"
	StateRefused    State = "refused"
)

type Conn struct {
	ctx          context.Context
	outputOnce   sync.Once
	output       *outputCredit
	bulkPacer    *bulkPacer
	heartbeatGap atomic.Int64
	peer         Identity
	transport    ssh.Conn
	states       chan State
	done         chan struct{}
	cancel       context.CancelFunc
	err          error
}

func (c *Conn) MaxHeartbeatGap() time.Duration { return time.Duration(c.heartbeatGap.Load()) }

func (c *Conn) Context() context.Context { return c.ctx }
func (c *Conn) Peer() Identity           { return c.peer }
func (c *Conn) States() <-chan State     { return c.states }
func (c *Conn) Done() <-chan struct{}    { return c.done }
func (c *Conn) Err() error               { <-c.done; return c.err }
func (c *Conn) Close() error             { c.cancel(); <-c.done; return nil }

func newConn(ctx context.Context, pacer *bulkPacer, transport ssh.Conn, channels <-chan ssh.NewChannel, handlers Handlers, channel ssh.Channel, decoder *json.Decoder, peer Identity, drains ...<-chan struct{}) *Conn {
	ctx, cancel := context.WithCancel(ctx)
	c := &Conn{ctx: ctx, bulkPacer: pacer, peer: peer, transport: transport, states: make(chan State, 1), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(c.done)
		c.run(ctx, transport, channels, handlers, channel, decoder)
		for _, drain := range drains {
			<-drain
		}
	}()
	return c
}

func (c *Conn) publish(state State) {
	select {
	case <-c.states:
	default:
	}
	c.states <- state
}

func (c *Conn) run(ctx context.Context, transport ssh.Conn, channels <-chan ssh.NewChannel, handlers Handlers, channel ssh.Channel, decoder *json.Decoder) {
	defer close(c.states)
	defer c.cancel()
	life := make(chan struct{})
	failed := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { c.dispatch(channels, handlers) })
	workers.Go(func() {
		for {
			var frame struct {
				Beat uint64 `json:"beat"`
			}
			if err := decoder.Decode(&frame); err != nil {
				select {
				case failed <- err:
				default:
				}
				return
			}
			select {
			case life <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	})
	workers.Go(func() {
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		encoder := json.NewEncoder(channel)
		for seq := uint64(1); ; seq++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := encoder.Encode(struct {
				Beat uint64 `json:"beat"`
			}{seq}); err != nil {
				select {
				case failed <- err:
				default:
				}
				return
			}
		}
	})
	stall := time.NewTimer(StallAfter)
	lost := time.NewTimer(LostAfter)
	defer stall.Stop()
	defer lost.Stop()
	c.publish(StateConnected)
	stalled := false
	lastHeartbeat := time.Now()
running:
	for {
		select {
		case <-ctx.Done():
			c.err = ctx.Err()
			break running
		case err := <-failed:
			c.err = fmt.Errorf("bridge to %s lost: %w", c.peer.DaemonID, err)
			break running
		case <-life:
			gap := time.Since(lastHeartbeat)
			lastHeartbeat = time.Now()
			if gap > time.Duration(c.heartbeatGap.Load()) {
				c.heartbeatGap.Store(int64(gap))
			}
			stall.Reset(StallAfter)
			lost.Reset(LostAfter)
			if stalled {
				stalled = false
				c.publish(StateConnected)
			}
		case <-stall.C:
			stalled = true
			c.publish(StateStalled)
		case <-lost.C:
			c.err = fmt.Errorf("bridge to %s lost: %w", c.peer.DaemonID, errNoFrames)
			break running
		}
	}
	pausepoint.At(pausepoint.BridgeConnectionClosed)
	c.cancel()
	c.bulkPacer.close()
	_ = transport.Close()
	workers.Wait()
	_ = transport.Wait()
	c.publish(StateLost)
}
