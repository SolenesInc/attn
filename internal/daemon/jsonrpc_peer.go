package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// Ids are per-direction; a frame with a method is a call, one without is an answer.
// Correlation is by the id's raw JSON text, so a child must echo the id verbatim.
type jsonrpcPeer struct {
	conn   net.Conn
	reader *bufio.Reader

	writeGate chan struct{}

	pendingMu sync.Mutex
	pending   map[string]chan jsonRPCMessage
	nextID    uint64
	closed    bool
}

func newJSONRPCPeer(conn net.Conn, reader *bufio.Reader) *jsonrpcPeer {
	return &jsonrpcPeer{
		conn:      conn,
		reader:    reader,
		writeGate: make(chan struct{}, 1),
		pending:   make(map[string]chan jsonRPCMessage),
	}
}

func (p *jsonrpcPeer) send(msg jsonRPCMessage) error {
	return p.sendContext(context.Background(), msg)
}

func (p *jsonrpcPeer) sendContext(ctx context.Context, msg jsonRPCMessage) error {
	select {
	case p.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.writeGate }()
	if err := ctx.Err(); err != nil {
		return err
	}

	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if err := p.conn.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("set write deadline: %w", err)
		}
	}
	var stop func() bool
	var interrupted chan struct{}
	if ctx.Done() != nil {
		interrupted = make(chan struct{})
		stop = context.AfterFunc(ctx, func() {
			_ = p.conn.SetWriteDeadline(time.Now())
			close(interrupted)
		})
	}

	err := json.NewEncoder(p.conn).Encode(msg)
	if stop != nil && !stop() {
		<-interrupted
		hasDeadline = true
	}
	if err != nil {
		// A failed write may have emitted a partial frame; the stream cannot be reused.
		_ = p.conn.Close()
		return err
	}
	if hasDeadline {
		if err := p.conn.SetWriteDeadline(time.Time{}); err != nil {
			_ = p.conn.Close()
			return fmt.Errorf("clear write deadline: %w", err)
		}
	}
	return nil
}

func (p *jsonrpcPeer) closePending(err error) {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for key, ch := range p.pending {
		delete(p.pending, key)
		ch <- jsonRPCMessage{
			Error: &jsonRPCError{Code: jsonRPCInternalError, Message: err.Error()},
		}
	}
}

func (p *jsonrpcPeer) routeResponse(msg jsonRPCMessage) bool {
	key := jsonRPCIDKey(msg.ID)
	if key == "" {
		return false
	}

	p.pendingMu.Lock()
	ch, exists := p.pending[key]
	if exists {
		delete(p.pending, key)
	}
	p.pendingMu.Unlock()
	if !exists {
		return false
	}
	ch <- msg
	return true
}

func (p *jsonrpcPeer) request(ctx context.Context, label, method string, params interface{}, result interface{}) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s request params: %w", label, err)
	}

	p.pendingMu.Lock()
	if p.closed {
		p.pendingMu.Unlock()
		return fmt.Errorf("%s connection is closed", label)
	}
	p.nextID++
	id := strconv.FormatUint(p.nextID, 10)
	responseCh := make(chan jsonRPCMessage, 1)
	p.pending[id] = responseCh
	p.pendingMu.Unlock()

	request := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  payload,
	}
	if err := p.sendContext(ctx, request); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return jsonRPCRequestContextError(ctx, label, method, "write", err)
	}

	select {
	case <-ctx.Done():
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return jsonRPCRequestContextError(ctx, label, method, "response", ctx.Err())
	case response := <-responseCh:
		if response.Error != nil {
			return fmt.Errorf("%s %s: %s", label, method, response.Error.Message)
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("%s %s returned no result", label, method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s %s result: %w", label, method, err)
		}
		return nil
	}
}

func jsonRPCRequestContextError(ctx context.Context, label, method, phase string, cause error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("%s %s %s canceled: %w", label, method, phase, context.Canceled)
	}
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline && (errors.Is(cause, os.ErrDeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return fmt.Errorf("%s %s %s deadline %s exceeded: %w", label, method, phase, deadline.Format(time.RFC3339Nano), context.DeadlineExceeded)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s %s %s canceled: %w", label, method, phase, err)
	}
	return fmt.Errorf("send %s %s request: %w", label, method, cause)
}
