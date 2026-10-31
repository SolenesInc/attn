package codexshared_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/codexshared"
	"nhooyr.io/websocket"
)

const requestOutgrowingSocketBuffers = 1 << 20

const disconnectingRounds = 20

func TestACallKeepsTheReplyItReceivedBeforeCodexDisconnected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply map[string]any
		check func(t *testing.T, result json.RawMessage, err error)
	}{
		{
			name:  "result",
			reply: map[string]any{"result": map[string]any{"accepted": true}},
			check: func(t *testing.T, result json.RawMessage, err error) {
				if err != nil || string(result) != `{"accepted":true}` {
					t.Fatalf("Call = %s, %v; want the received result", result, err)
				}
			},
		},
		{
			name:  "rpc error",
			reply: map[string]any{"error": map[string]any{"code": -32603, "message": "refused"}},
			check: func(t *testing.T, _ json.RawMessage, err error) {
				var refused *codexshared.RPCError
				if !errors.As(err, &refused) || string(refused.Reply) != `{"code":-32603,"message":"refused"}` {
					t.Fatalf("Call error = %v; want the received RPC error", err)
				}
			},
		},
		{
			name: "unanswered",
			check: func(t *testing.T, result json.RawMessage, err error) {
				var refused *codexshared.RPCError
				if err == nil || errors.As(err, &refused) {
					t.Fatalf("Call = %s, %v; want the disconnect error", result, err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range disconnectingRounds {
				result, err := callWhileCodexRepliesAndDisconnects(t, tc.reply)
				tc.check(t, result, err)
			}
		})
	}
}

type callOutcome struct {
	result json.RawMessage
	err    error
}

func callWhileCodexRepliesAndDisconnects(t *testing.T, reply map[string]any) (json.RawMessage, error) {
	t.Helper()
	codex := startDisconnectingCodex(t, reply)
	client, err := codexshared.Connect(context.Background(), codex.socket, codexshared.ClientInfo{Name: "test", Version: "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	outcome := make(chan callOutcome, 1)
	go func() {
		result, err := client.Call(context.Background(), "test/large", map[string]string{"payload": strings.Repeat("x", requestOutgrowingSocketBuffers)})
		outcome <- callOutcome{result, err}
	}()

	<-client.Done()
	select {
	case early := <-outcome:
		t.Fatalf("Call returned (%s, %v) before Codex disconnected: a %d-byte request no longer outgrows the socket buffers", early.result, early.err, requestOutgrowingSocketBuffers)
	default:
	}
	close(codex.drain)
	got := <-outcome
	return got.result, got.err
}

type disconnectingCodex struct {
	socket string
	drain  chan struct{}
}

type connKey struct{}

func startDisconnectingCodex(t *testing.T, reply map[string]any) disconnectingCodex {
	t.Helper()
	dir, err := os.MkdirTemp("", "codexrpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	codex := disconnectingCodex{socket: filepath.Join(dir, "codex.sock"), drain: make(chan struct{})}
	listener, err := net.Listen("unix", codex.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		ConnContext: func(ctx context.Context, c net.Conn) context.Context { return context.WithValue(ctx, connKey{}, c) },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Context().Value(connKey{}).(*net.UnixConn)
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			ws.SetReadLimit(-1)
			defer ws.CloseNow()
			ctx := context.Background()

			_, data, err := ws.Read(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			var initialize codexshared.Message
			if err := json.Unmarshal(data, &initialize); err != nil {
				t.Error(err)
				return
			}
			send(t, ws, map[string]any{"id": initialize.ID, "result": map[string]any{"userAgent": "codex/0.0.0"}})
			if _, _, err := ws.Read(ctx); err != nil {
				t.Error(err)
				return
			}

			_, request, err := ws.Reader(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			id, err := requestID(request)
			if err != nil {
				t.Error(err)
				return
			}
			if reply != nil {
				answer := map[string]any{"id": id}
				for k, v := range reply {
					answer[k] = v
				}
				send(t, ws, answer)
			}
			if err := raw.CloseWrite(); err != nil {
				t.Error(err)
				return
			}
			<-codex.drain
			if _, err := io.Copy(io.Discard, request); err != nil {
				t.Error(err)
			}
		}),
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return codex
}

func requestID(request io.Reader) (json.RawMessage, error) {
	prefix := make([]byte, len(`{"id":`))
	if _, err := io.ReadFull(request, prefix); err != nil {
		return nil, err
	}
	if string(prefix) != `{"id":` {
		return nil, errors.New("request does not start with its id: " + string(prefix))
	}
	var id []byte
	for {
		b := make([]byte, 1)
		if _, err := io.ReadFull(request, b); err != nil {
			return nil, err
		}
		if b[0] == ',' {
			return json.RawMessage(bytes.TrimSpace(id)), nil
		}
		id = append(id, b[0])
	}
}

func send(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Error(err)
	}
}
