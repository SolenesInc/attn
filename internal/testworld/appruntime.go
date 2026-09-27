package testworld

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
)

const appRuntimeAPIVersion = 5

type FakeAppRuntime struct {
	t    testing.TB
	fifo string
}

func NewFakeAppRuntime(t testing.TB) *FakeAppRuntime {
	t.Helper()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "generation")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(dir, "attn-app-runtime")
	script := "#!/bin/sh\necho \"$ATTN_APP_RUNTIME_GENERATION\" > '" + fifo + "'\nexec sleep 86400\n"
	if err := os.WriteFile(host, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_APP_RUNTIME_HOST", host)
	return &FakeAppRuntime{t: t, fifo: fifo}
}

type AppRuntimeConn struct {
	t      testing.TB
	conn   net.Conn
	reader *bufio.Reader
	calls  int
}

type AppDispatchEvent struct {
	App      string          `json:"-"`
	Dispatch string          `json:"-"`
	Name     string          `json:"name"`
	Subject  string          `json:"subject"`
	Seq      int64           `json:"seq"`
	Payload  json.RawMessage `json:"payload"`
}

func (r *FakeAppRuntime) Connect(dial func() (net.Conn, error)) *AppRuntimeConn {
	r.t.Helper()
	launched := make(chan string, 1)
	go func() {
		body, _ := os.ReadFile(r.fifo)
		launched <- strings.TrimSpace(string(body))
	}()
	var generation uint64
	select {
	case got := <-launched:
		parsed, err := strconv.ParseUint(got, 10, 64)
		if err != nil {
			r.t.Fatalf("the daemon launched the app runtime with generation %q", got)
		}
		generation = parsed
	case <-time.After(fakeagent.HangGuard):
		r.t.Fatalf("the daemon did not launch the app runtime within %s", fakeagent.HangGuard)
	}
	conn, err := dial()
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = conn.Close() })
	c := &AppRuntimeConn{t: r.t, conn: conn, reader: bufio.NewReader(conn)}
	c.send(map[string]any{"jsonrpc": "2.0", "id": "hello", "method": "app_runtime.hello",
		"params": map[string]any{"generation": generation, "api_version": appRuntimeAPIVersion, "pid": os.Getpid()}})
	if answer := c.read(); answer.Error != nil {
		r.t.Fatalf("the daemon refused the app runtime's hello: %s", answer.Error)
	}
	return c
}

func (c *AppRuntimeConn) NextDispatch() AppDispatchEvent {
	c.t.Helper()
	event, release := c.HoldDispatch()
	release()
	return event
}

func (c *AppRuntimeConn) HoldDispatch() (AppDispatchEvent, func()) {
	c.t.Helper()
	for {
		msg := c.read()
		if msg.Method != "app.dispatch" {
			c.answer(msg)
			continue
		}
		var params struct {
			Dispatch string           `json:"dispatch"`
			App      string           `json:"app"`
			Event    AppDispatchEvent `json:"event"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			c.t.Fatalf("decode app.dispatch %s: %v", msg.Params, err)
		}
		params.Event.App, params.Event.Dispatch = params.App, params.Dispatch
		return params.Event, func() { c.answer(msg) }
	}
}

func (c *AppRuntimeConn) Call(method string, params any) json.RawMessage {
	c.t.Helper()
	c.calls++
	id := "call-" + strconv.Itoa(c.calls)
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		msg := c.read()
		if msg.Method != "" {
			c.answer(msg)
			continue
		}
		if string(msg.ID) != strconv.Quote(id) {
			continue
		}
		if msg.Error != nil {
			c.t.Fatalf("%s as the app runtime: %s", method, msg.Error)
		}
		return msg.Result
	}
}

func (c *AppRuntimeConn) answer(msg appRuntimeMessage) {
	c.t.Helper()
	if msg.Method != "" {
		c.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"ok": true}})
	}
}

type appRuntimeMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (c *AppRuntimeConn) send(msg any) {
	c.t.Helper()
	encoded, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(append(encoded, '\n')); err != nil {
		c.t.Fatalf("write to the daemon as the app runtime: %v", err)
	}
}

func (c *AppRuntimeConn) read() appRuntimeMessage {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		c.t.Fatal(err)
	}
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read from the daemon as the app runtime: %v", err)
	}
	var msg appRuntimeMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		c.t.Fatalf("decode %q: %v", line, err)
	}
	return msg
}
