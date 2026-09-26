package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func writeWS(conn *websocket.Conn, msg map[string]interface{}) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, payload)
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type %T", listener.Addr())
	}
	return addr.Port, nil
}

func useFreeWSPort(t *testing.T) string {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate WebSocket port: %v", err)
	}
	value := strconv.Itoa(port)
	t.Setenv("ATTN_WS_PORT", value)
	return value
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

func asBool(v interface{}) bool {
	b, ok := v.(bool)
	return ok && b
}
