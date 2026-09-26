package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type recordingDoorbell struct {
	mu     sync.Mutex
	writes []string
}

func (r *recordingDoorbell) backend() *fakeSpawnBackend {
	return &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.writes = append(r.writes, string(data))
	}}
}

func (r *recordingDoorbell) pasted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	prompts := []string{}
	for _, write := range r.writes {
		if !strings.HasPrefix(write, sessionInputPasteStart) {
			continue
		}
		prompts = append(prompts, strings.TrimSuffix(strings.TrimPrefix(write, sessionInputPasteStart), sessionInputPasteEnd))
	}
	return prompts
}

func newAgentMsgDaemon(t *testing.T) (*Daemon, *recordingDoorbell) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(func() {
		d.sessionInputs().stopRetries()
		d.stopAgentMailboxDoorbells()
		_ = d.store.Close()
	})
	doorbell := &recordingDoorbell{}
	d.ptyBackend = doorbell.backend()
	return d, doorbell
}

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

func TestHandleAgentMsgQueuesUnderApprovalAndDrainsOnTheNextStateChange(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStatePendingApproval)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "when you surface, rebase")
	result := resp.AgentMsgResult
	if result == nil || result.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Detail, "approval") {
		t.Fatalf("detail does not say why it waits: %q", result.Detail)
	}
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("typed into a session waiting on an approval: %q", prompts)
	}

	drains := observeAgentMailboxDrains(t, d)
	if !d.applyState(sessionStateChange{
		sessionID: "target-session-id",
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("applyState did not apply")
	}

	if delivered := drains.next(); delivered != 1 {
		t.Fatalf("drain delivered %d messages, want 1", delivered)
	}
	prompts := doorbell.pasted()
	if len(prompts) != 1 {
		t.Fatalf("typed %d prompts after the drain, want 1: %q", len(prompts), prompts)
	}
	prompt := prompts[0]
	if prompt != agentMailboxDoorbellText {
		t.Fatalf("drained doorbell = %q", prompt)
	}
	queued, err := d.store.UnreadAgentMailboxDeliveries("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Item.NotifiedAt == "" {
		t.Fatalf("unread item was not stamped after the drain: %+v", queued)
	}
}

func newHeldDoorbellDaemon(t *testing.T) (*Daemon, *recordingDoorbell, chan int) {
	t.Helper()
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	drained := make(chan int, 1)
	d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
	return d, doorbell, drained
}

func typeIntoTarget(t *testing.T, d *Daemon) {
	t.Helper()
	if err := d.writeSessionPTY("target-session-id", []byte("a draft"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
}

func TestHandleAgentMsgHeldOffByTypingLandsAfterTheQuietWindow(t *testing.T) {
	d, doorbell, drained := newHeldDoorbellDaemon(t)
	quiesceTranscriptWatchers(t, d)
	synctest.Test(t, func(t *testing.T) {
		defer d.stopAgentMailboxDoorbells()
		typeIntoTarget(t, d)

		resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "the migration landed")
		result := resp.AgentMsgResult
		if result == nil || result.Status != protocol.AgentMsgStatusQueued || !strings.Contains(result.Detail, "typed") {
			t.Fatalf("result = %+v", result)
		}
		if prompts := doorbell.pasted(); len(prompts) != 0 {
			t.Fatalf("typed into a composer the user just used: %q", prompts)
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		select {
		case delivered := <-drained:
			if delivered != 1 {
				t.Fatalf("drain delivered %d, want 1", delivered)
			}
		default:
			t.Fatal("nothing retried the delivery once the composer went quiet")
		}
		if prompts := doorbell.pasted(); len(prompts) != 1 || prompts[0] != agentMailboxDoorbellText {
			t.Fatalf("doorbells after the window = %q", doorbell.pasted())
		}
	})
}
