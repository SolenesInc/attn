package testworld

import (
	"encoding/json"
	"strconv"
)

func (c *AppRuntimeConn) TryCall(method string, params any) (json.RawMessage, string) {
	c.t.Helper()
	c.calls++
	id := "try-" + strconv.Itoa(c.calls)
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
			var refusal struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(msg.Error, &refusal)
			return nil, refusal.Message
		}
		return msg.Result, ""
	}
}
