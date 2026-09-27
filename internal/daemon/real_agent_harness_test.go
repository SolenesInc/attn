package daemon

import (
	"fmt"
	"net"
	"strconv"
	"testing"
)

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
