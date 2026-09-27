package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDelegateMessageRejectsRetiredWireFields(t *testing.T) {
	for _, field := range []string{"brief", "ticket_id", "confirm", "placement", "workspace_id", "worktree", "plot", "handover", "preferences_revision", "approval_policy", "sandbox_mode"} {
		body := `{"cmd":"delegate","request_id":"mixed","assignment":{"kind":"new","brief":"work"},"cwd":"/tmp","` + field + `":null}`
		var msg DelegateMessage
		err := json.Unmarshal([]byte(body), &msg)
		if err == nil || !strings.Contains(err.Error(), `field "`+field+`" is retired`) {
			t.Fatalf("%s: error=%v", field, err)
		}
	}
}
