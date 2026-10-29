package nisse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const Protocol = 7

const ackMethod = "@effect/rpc/Ack"

func Socket(home string) string { return filepath.Join(home, "nisse.sock") }

type Error struct {
	Method  string
	Tag     string
	Message string
}

func (e *Error) Error() string {
	if e.Tag != "" {
		return fmt.Sprintf("nisse %s: %s", e.Method, e.Tag)
	}
	return fmt.Sprintf("nisse %s: %s", e.Method, e.Message)
}

var ErrClosed = errors.New("the connection to nisse closed")

type message struct {
	ID     *uint64         `json:"id,omitempty"`
	Chunk  bool            `json:"chunk,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
		Data    []struct {
			Error struct {
				Tag string `json:"_tag"`
			} `json:"error"`
		} `json:"data"`
	} `json:"error,omitempty"`
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type pending struct {
	method string
	items  func(json.RawMessage)
	done   chan message
}

type Client struct {
	conn    net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[uint64]*pending
	next    atomic.Uint64
	done    chan struct{}
	version string
}

func Connect(ctx context.Context, socket, client, version string) (*Client, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, pending: make(map[uint64]*pending), done: make(chan struct{})}
	go c.read()
	var hello struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := c.Call(ctx, "initialize", map[string]any{"client": client, "version": version, "protocol": Protocol}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	if hello.Protocol < Protocol {
		c.Close()
		return nil, fmt.Errorf("nisse %s speaks protocol %d; attn needs %d or later", hello.Version, hello.Protocol, Protocol)
	}
	c.version = hello.Version
	return c, nil
}

func (c *Client) Version() string { return c.version }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() { _ = c.conn.Close() }

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	reply, err := c.send(ctx, method, params, nil)
	if err != nil || result == nil || len(reply) == 0 {
		return err
	}
	return json.Unmarshal(reply, result)
}

func (c *Client) Stream(ctx context.Context, method string, params any, item func(json.RawMessage)) error {
	_, err := c.send(ctx, method, params, item)
	return err
}

func (c *Client) send(ctx context.Context, method string, params any, items func(json.RawMessage)) (json.RawMessage, error) {
	id := c.next.Add(1)
	p := &pending{method: method, items: items, done: make(chan message, 1)}
	c.mu.Lock()
	c.pending[id] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.write(request{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case m := <-p.done:
		if m.Error != nil {
			failure := &Error{Method: method, Message: m.Error.Message}
			if len(m.Error.Data) > 0 {
				failure.Tag = m.Error.Data[0].Error.Tag
			}
			return nil, failure
		}
		return m.Result, nil
	case <-c.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) write(r request) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.conn.Write(append(line, '\n'))
	return err
}

func (c *Client) read() {
	defer close(c.done)
	defer c.conn.Close()
	lines := bufio.NewScanner(c.conn)
	lines.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for lines.Scan() {
		var m message
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		c.mu.Lock()
		p := c.pending[*m.ID]
		c.mu.Unlock()
		if p == nil {
			continue
		}
		if !m.Chunk {
			p.done <- m
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(m.Result, &items) == nil && p.items != nil {
			for _, item := range items {
				p.items(item)
			}
		}
		if c.write(request{JSONRPC: "2.0", Method: ackMethod, Params: map[string]uint64{"requestId": *m.ID}}) != nil {
			return
		}
	}
}
