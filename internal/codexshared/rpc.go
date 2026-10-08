package codexshared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"nhooyr.io/websocket"
)

// The server sends no "jsonrpc" field and ignores one.
type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type RPCError struct {
	Method string
	Reply  json.RawMessage
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex %s: %s", e.Method, e.Reply) }

func Dial(ctx context.Context, path string) (*websocket.Conn, error) {
	// The server links long socket paths into /tmp; connecting by the link's own path overflows sun_path.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	conn, _, err := websocket.Dial(ctx, "http://localhost/rpc", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	conn.SetReadLimit(-1)
	return conn, nil
}

type Client struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	pending map[string]chan Message
	next    atomic.Uint64
	done    chan struct{}
	err     error
}

func Connect(ctx context.Context, path, name string, observe func(Message)) (*Client, error) {
	conn, err := Dial(ctx, path)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, pending: make(map[string]chan Message), done: make(chan struct{})}
	go c.read(observe)
	_, err = c.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": name, "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err == nil {
		err = c.write(ctx, Message{Method: "initialized"})
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) read(observe func(Message)) {
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Method == "" && len(m.ID) > 0 {
			c.mu.Lock()
			reply := c.pending[string(m.ID)]
			c.mu.Unlock()
			if reply != nil {
				reply <- m
			}
			continue
		}
		if observe != nil {
			observe(m)
		}
	}
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := strconv.FormatUint(c.next.Add(1), 10)
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	reply := make(chan Message, 1)
	c.mu.Lock()
	c.pending[id] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.write(ctx, Message{ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		return nil, err
	}
	select {
	case m := <-reply:
		if len(m.Error) > 0 {
			return nil, &RPCError{Method: method, Reply: m.Error}
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("codex disconnected")
		}
		return nil, err
	}
}

func (c *Client) Respond(ctx context.Context, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.write(ctx, Message{ID: id, Result: raw})
}

func (c *Client) write(ctx context.Context, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *Client) Close() { _ = c.conn.CloseNow() }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Connected() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func Proxy(ctx context.Context, down, up *websocket.Conn, prepare func(*Message) (func(*Message), error), observe func(Message)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	pending := make(map[string]func(*Message))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		for {
			kind, data, err := up.Read(ctx)
			if err != nil {
				return
			}
			var m Message
			if json.Unmarshal(data, &m) == nil {
				if m.Method == "" && len(m.ID) > 0 {
					mu.Lock()
					callback := pending[string(m.ID)]
					delete(pending, string(m.ID))
					mu.Unlock()
					if callback != nil {
						callback(&m)
						data = rewriteReply(data, m)
					}
				}
				if observe != nil {
					observe(m)
				}
			}
			if down.Write(ctx, kind, data) != nil {
				return
			}
		}
	}()
	for {
		kind, data, err := down.Read(ctx)
		if err != nil {
			break
		}
		var m Message
		if json.Unmarshal(data, &m) == nil && m.Method != "" {
			callback, err := prepare(&m)
			if err != nil {
				refusal, _ := json.Marshal(Message{ID: m.ID, Error: mustJSON(map[string]any{"code": -32603, "message": err.Error()})})
				if down.Write(ctx, websocket.MessageText, refusal) != nil {
					break
				}
				continue
			}
			if callback != nil && len(m.ID) > 0 {
				mu.Lock()
				pending[string(m.ID)] = callback
				mu.Unlock()
			}
			if data, err = rewriteParams(data, m); err != nil {
				break
			}
		}
		if up.Write(ctx, kind, data) != nil {
			break
		}
	}
	cancel()
	_ = up.CloseNow()
	_ = down.CloseNow()
	<-done
}

func rewriteParams(data []byte, m Message) ([]byte, error) {
	var original map[string]json.RawMessage
	if err := json.Unmarshal(data, &original); err != nil {
		return nil, err
	}
	if len(m.Params) > 0 {
		original["params"] = m.Params
	}
	return json.Marshal(original)
}

func rewriteReply(data []byte, m Message) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return data
	}
	delete(envelope, "result")
	delete(envelope, "error")
	if len(m.Result) > 0 {
		envelope["result"] = m.Result
	}
	if len(m.Error) > 0 {
		envelope["error"] = m.Error
	}
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return data
	}
	return rewritten
}

func mustJSON(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
