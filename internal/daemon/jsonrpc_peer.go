package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
)

// Ids are per-direction; a frame with a method is a call, one without is an answer.
// Correlation is by the id's raw JSON text, so a child must echo the id verbatim.
type jsonrpcPeer struct {
	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan jsonRPCMessage
	nextID    uint64
	closed    bool
}

func newJSONRPCPeer(conn net.Conn, reader *bufio.Reader) *jsonrpcPeer {
	return &jsonrpcPeer{
		conn:    conn,
		reader:  reader,
		pending: make(map[string]chan jsonRPCMessage),
	}
}

func (p *jsonrpcPeer) send(msg jsonRPCMessage) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return json.NewEncoder(p.conn).Encode(msg)
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
	if err := p.send(request); err != nil {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return fmt.Errorf("send %s request: %w", label, err)
	}

	select {
	case <-ctx.Done():
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
		return ctx.Err()
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
