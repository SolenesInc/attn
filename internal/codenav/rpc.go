package codenav

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

// maxMessageBytes is past the largest answer a language server sends: the
// 2,362 uses of protocol.Ptr, attn's most used name, come to 547 KB.
const maxMessageBytes = 64 << 20

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// conn speaks JSON-RPC with Content-Length framing over a server's stdio.
type conn struct {
	w       io.Writer
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[string]chan rpcMessage
	closed  error
}

// serve reads messages until the stream ends, answering each server request
// with handle and passing each notification to notify.
func (c *conn) serve(r io.Reader, handle func(method string, params json.RawMessage) any, notify func(method string, params json.RawMessage)) error {
	reader := bufio.NewReader(r)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			c.fail(err)
			return err
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			_ = c.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(handle(msg.Method, msg.Params))})
		case msg.Method != "":
			notify(msg.Method, msg.Params)
		default:
			c.mu.Lock()
			reply := c.pending[string(msg.ID)]
			delete(c.pending, string(msg.ID))
			c.mu.Unlock()
			if reply != nil {
				reply <- msg
			}
		}
	}
}

func (c *conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed == nil {
		c.closed = err
	}
	for id, reply := range c.pending {
		close(reply)
		delete(c.pending, id)
	}
}

func (c *conn) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed != nil {
		err := c.closed
		c.mu.Unlock()
		return fmt.Errorf("the language server's stream ended: %w", err)
	}
	c.nextID++
	id := json.RawMessage(strconv.Itoa(c.nextID))
	reply := make(chan rpcMessage, 1)
	if c.pending == nil {
		c.pending = map[string]chan rpcMessage{}
	}
	c.pending[string(id)] = reply
	c.mu.Unlock()
	if err := c.write(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: mustJSON(params)}); err != nil {
		return err
	}
	select {
	case msg, ok := <-reply:
		if !ok {
			c.mu.Lock()
			err := c.closed
			c.mu.Unlock()
			return fmt.Errorf("the language server's stream ended: %w", err)
		}
		if msg.Error != nil {
			return msg.Error
		}
		if result == nil || len(msg.Result) == 0 {
			return nil
		}
		return json.Unmarshal(msg.Result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		_ = c.notify("$/cancelRequest", map[string]json.RawMessage{"id": id})
		return ctx.Err()
	}
}

func (c *conn) notify(method string, params any) error {
	return c.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustJSON(params)})
}

func (c *conn) write(msg rpcMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

func readMessage(r *bufio.Reader) (rpcMessage, error) {
	header, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		return rpcMessage{}, err
	}
	length, err := strconv.Atoi(strings.TrimSpace(header.Get("Content-Length")))
	if err != nil {
		return rpcMessage{}, fmt.Errorf("a message with no Content-Length: %v", header)
	}
	if length > maxMessageBytes {
		return rpcMessage{}, fmt.Errorf("a message of %d bytes; attn reads language server messages up to %d bytes", length, maxMessageBytes)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return rpcMessage{}, fmt.Errorf("a message that is not JSON: %w", err)
	}
	return msg, nil
}

func mustJSON(value any) json.RawMessage {
	if value == nil {
		return json.RawMessage("null")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
