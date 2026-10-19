package daemon

import (
	"encoding/json"
	"net"
	"strconv"
	"testing"
)

func registerTestPluginDriver(t *testing.T, conn net.Conn, agent string, capabilities map[string]bool) {
	t.Helper()
	sendPluginMethod(t, conn, 2, "driver.register", pluginDriverRegisterParams{
		Agent:        agent,
		Capabilities: capabilities,
	})
}

func sendPluginMethod(t *testing.T, conn net.Conn, id int, method string, params interface{}) {
	t.Helper()
	response := sendPluginMethodResponse(t, conn, id, method, params)
	if response.Error != nil {
		t.Fatalf("%s error=%#v", method, response.Error)
	}
}

func sendPluginMethodResponse(t *testing.T, conn net.Conn, id int, method string, params interface{}) jsonRPCMessage {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage([]byte(strconv.Itoa(id))),
		Method:  method,
		Params:  payload,
	}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
	return decodeJSONRPCMessage(t, conn)
}
