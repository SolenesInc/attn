package daemon

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func callAgentMsg(t *testing.T, d *Daemon, target, source, content string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentMsg(conn, &protocol.AgentMsgMessage{
			Cmd:             protocol.CmdAgentMsg,
			TargetSessionID: target,
			SourceSessionID: source,
			Content:         content,
		})
	})
}

func TestHandleAgentMsgFailedWakeLeavesNoUndeliverableMessage(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	backend.spawnErr = errors.New("the harness would not start")
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "keel", "sender-session-id", "please wake")
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "would not start") {
		t.Fatalf("response = %+v", resp)
	}
	targets, err := d.store.TargetsWithUnreadAgentMailboxItems()
	if err != nil || len(targets) != 0 {
		t.Fatalf("failed wake left an undeliverable row: %v, %v", targets, err)
	}
	if binding := memberByID(t, crewList(t, d), "keel").BindingSession; binding != nil {
		t.Fatalf("failed wake left keel bound to %q", *binding)
	}
}
