package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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
	t.Cleanup(func() { _ = d.store.Close() })
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

func TestHandleAgentMsgNotifiesWithAnAttributedContentFreeDoorbell(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentCodex, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "  the migration landed  ")
	if !resp.Ok || resp.AgentMsgResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentMsgResult
	if result.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("status = %q detail = %q", result.Status, result.Detail)
	}
	if result.MessageID == "" || result.TargetSessionID != "target-session-id" {
		t.Fatalf("result = %+v", result)
	}

	prompts := doorbell.pasted()
	if len(prompts) != 1 {
		t.Fatalf("typed %d prompts, want 1: %q", len(prompts), prompts)
	}
	prompt := prompts[0]
	for _, want := range []string{
		"📨 session sender-s (workspace-sender-session-id) sent message " + result.MessageID,
		"attn agent inbox " + result.MessageID,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the migration landed") {
		t.Fatalf("doorbell leaked the message body:\n%s", prompt)
	}

	queued, err := d.store.QueuedAgentMailboxDeliveries("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("a notified message is still queued: %+v", queued)
	}
}

func TestHandleAgentMsgResolvesAnAwakeCrewMemberToItsLiveSession(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "keels-live-session", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	if _, err := d.claimCrewBinding("keel", "keels-live-session"); err != nil {
		t.Fatalf("bind keel: %v", err)
	}
	doorbell := &recordingDoorbell{}
	backend.onInput = doorbell.backend().onInput

	resp := callAgentMsg(t, d, "Keel", "sender-session-id", "the garden is ready")
	if !resp.Ok || resp.AgentMsgResult == nil || resp.AgentMsgResult.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("response = %+v", resp)
	}
	if resp.AgentMsgResult.TargetSessionID != "keels-live-session" {
		t.Fatalf("target = %q, want keel's live day", resp.AgentMsgResult.TargetSessionID)
	}
	if resp.AgentMsgResult.Detail != "notified Keel" {
		t.Fatalf("detail = %q, want the member's display name", resp.AgentMsgResult.Detail)
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 || strings.Contains(prompts[0], "the garden is ready") ||
		!strings.Contains(prompts[0], "attn agent inbox "+resp.AgentMsgResult.MessageID) {
		t.Fatalf("doorbell prompts = %q", prompts)
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 0 {
		t.Fatalf("messaging an awake member spawned %d sessions", spawned)
	}
}

func TestHandleAgentMsgWakesASleepingMemberBeforeNotifyingIt(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	var initialPrompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		body, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		initialPrompt = string(body)
	}
	writes := 0
	backend.onInput = func(_ string, _ []byte) { writes++ }

	resp := callAgentMsg(t, d, "trellis", "sender-session-id", "please inspect the broken build")
	if !resp.Ok || resp.AgentMsgResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentMsgResult
	if result.Status != protocol.AgentMsgStatusQueued || result.MessageID == "" || result.TargetSessionID == "" {
		t.Fatalf("result = %+v, want a durable queued delivery to the new day", result)
	}
	if !strings.Contains(result.Detail, "woke Trellis") {
		t.Fatalf("detail = %q, want the member's display name", result.Detail)
	}
	if !strings.Contains(initialPrompt, crewWakePrompt) {
		t.Fatalf("the wake prompt was replaced by peer content:\n%s", initialPrompt)
	}
	if strings.Contains(initialPrompt, "please inspect the broken build") || strings.Contains(initialPrompt, "attn agent inbox") {
		t.Fatalf("peer content entered the initial prompt:\n%s", initialPrompt)
	}
	if writes != 0 {
		t.Fatalf("the message was pasted %d times in addition to the initial prompt", writes)
	}
	queued, err := d.store.QueuedAgentMailboxDeliveries(result.TargetSessionID)
	if err != nil || len(queued) != 1 || queued[0].Item.ID != result.MessageID {
		t.Fatalf("message was not durable before the wake completed: queued=%+v err=%v", queued, err)
	}

	drained := make(chan int, 1)
	d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
	hook := callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{ID: result.TargetSessionID, State: protocol.StateWorking})
	})
	if !hook.Ok {
		t.Fatalf("ready-state hook: %+v", hook)
	}
	if delivered := <-drained; delivered != 1 {
		t.Fatalf("ready-state drain delivered %d messages, want 1", delivered)
	}
	queued, err = d.store.QueuedAgentMailboxDeliveries(result.TargetSessionID)
	if err != nil || len(queued) != 0 {
		t.Fatalf("the ready-state hook left the notification queued: %+v, %v", queued, err)
	}
	if writes == 0 {
		t.Fatal("the ready-state hook did not place the notification")
	}
}

func TestHandleAgentMsgDuringWakePrimingDrainsAfterTheInitialPrompt(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	doorbell := &recordingDoorbell{}
	backend.onInput = doorbell.backend().onInput

	first := callAgentMsg(t, d, "keel", "sender-session-id", "first ask")
	if !first.Ok || first.AgentMsgResult == nil {
		t.Fatalf("first response = %+v", first)
	}
	sessionID := first.AgentMsgResult.TargetSessionID
	second := callAgentMsg(t, d, "keel", "sender-session-id", "second ask")
	if !second.Ok || second.AgentMsgResult == nil || second.AgentMsgResult.TargetSessionID != sessionID || second.AgentMsgResult.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("second response = %+v, want a queue behind the same waking day", second)
	}
	queued, err := d.store.QueuedAgentMailboxDeliveries(sessionID)
	if err != nil || len(queued) != 1 || queued[0].Item.ID != first.AgentMsgResult.MessageID {
		t.Fatalf("messages queued during priming = %+v, %v", queued, err)
	}
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("a message jumped ahead of priming: %q", prompts)
	}

	scheduled := make(chan string, 1)
	drained := make(chan int, 1)
	d.agentMailboxDrainScheduledHook = func(sessionID string) { scheduled <- sessionID }
	d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
	hook := callHandler(t, func(conn net.Conn) {
		d.handleState(conn, &protocol.StateMessage{ID: sessionID, State: protocol.StateWorking})
	})
	if !hook.Ok {
		t.Fatalf("ready-state hook: %+v", hook)
	}
	select {
	case got := <-scheduled:
		if got != sessionID {
			t.Fatalf("drain scheduled for %q, want %q", got, sessionID)
		}
	default:
		t.Fatal("ready-state hook did not open the queued-message drain")
	}
	if delivered := <-drained; delivered != 1 {
		t.Fatalf("drained %d messages behind the initial prompt, want 1", delivered)
	}
	queued, err = d.store.QueuedAgentMailboxDeliveries(sessionID)
	if err != nil || len(queued) != 0 {
		t.Fatalf("queue after first notification = %+v, %v", queued, err)
	}
	prompts := doorbell.pasted()
	if len(prompts) != 1 || !strings.Contains(prompts[0], first.AgentMsgResult.MessageID) ||
		strings.Contains(prompts[0], "first ask") || strings.Contains(prompts[0], "second ask") {
		t.Fatalf("doorbell prompts after priming = %q", prompts)
	}
	secondRecord, err := d.store.PeerMessageRecord(second.AgentMsgResult.MessageID)
	if err != nil || secondRecord.State() != agentmailbox.StateQueued {
		t.Fatalf("second message = %+v, %v", secondRecord, err)
	}
}

func TestHandleAgentMsgWakeLimitRefusalDeliversNothing(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "0")
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "alder", "sender-session-id", "wake up")
	if resp.Ok {
		t.Fatalf("wake past the limit succeeded: %+v", resp)
	}
	detail := protocol.Deref(resp.Error)
	for _, want := range []string{"crew.wake_limit=0", "Alder", "sidebar", "nothing was delivered"} {
		if !strings.Contains(detail, want) {
			t.Errorf("refusal %q does not name %q", detail, want)
		}
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 0 {
		t.Fatalf("the refused wake spawned %d sessions", spawned)
	}
	queued, err := d.store.TargetsWithQueuedAgentMailboxItems()
	if err != nil || len(queued) != 0 {
		t.Fatalf("the refused wake queued a message anyway: %v, %v", queued, err)
	}
}

func TestHandleAgentMsgFailedWakeLeavesNoUndeliverableMessage(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	backend.spawnErr = errors.New("the harness would not start")
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "keel", "sender-session-id", "please wake")
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "would not start") {
		t.Fatalf("response = %+v", resp)
	}
	targets, err := d.store.TargetsWithQueuedAgentMailboxItems()
	if err != nil || len(targets) != 0 {
		t.Fatalf("failed wake left an undeliverable row: %v, %v", targets, err)
	}
	if binding := memberByID(t, crewList(t, d), "keel").BindingSession; binding != nil {
		t.Fatalf("failed wake left keel bound to %q", *binding)
	}
}

func TestHandleAgentMsgUnknownAddressNamesBothPlacesToLook(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentMsg(t, d, "nobody", "sender-session-id", "hello")
	if resp.Ok || protocol.Deref(resp.ErrorCode) != "session_or_crew_member_not_found" {
		t.Fatalf("response = %+v", resp)
	}
	for _, want := range []string{`"nobody"`, "attn agent list", "attn crew list"} {
		if !strings.Contains(protocol.Deref(resp.Error), want) {
			t.Errorf("error %q does not name %q", protocol.Deref(resp.Error), want)
		}
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 0 {
		t.Fatalf("an unknown address spawned %d sessions", spawned)
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

	drained := make(chan int, 1)
	d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
	if !d.applyState(sessionStateChange{
		sessionID: "target-session-id",
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("applyState did not apply")
	}

	if delivered := <-drained; delivered != 1 {
		t.Fatalf("drain delivered %d messages, want 1", delivered)
	}
	prompts := doorbell.pasted()
	if len(prompts) != 1 {
		t.Fatalf("typed %d prompts after the drain, want 1: %q", len(prompts), prompts)
	}
	prompt := prompts[0]
	if strings.Contains(prompt, "when you surface, rebase") || !strings.Contains(prompt, "attn agent inbox "+result.MessageID) {
		t.Fatalf("drained doorbell = %q", prompt)
	}
	queued, err := d.store.QueuedAgentMailboxDeliveries("target-session-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("still queued after the drain: %+v", queued)
	}
}

func TestHandleAgentMsgRefusalsNameTheirReason(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	refusal := func(t *testing.T, target, source, content string) string {
		t.Helper()
		resp := callAgentMsg(t, d, target, source, content)
		if resp.AgentMsgResult == nil || resp.AgentMsgResult.Status != protocol.AgentMsgStatusRefused {
			t.Fatalf("expected a refusal, got %+v", resp.AgentMsgResult)
		}
		return resp.AgentMsgResult.Detail
	}

	if detail := refusal(t, "target-session-id", "sender-session-id", "   "); !strings.Contains(detail, "empty") {
		t.Fatalf("empty message detail = %q", detail)
	}
	oversize := refusal(t, "target-session-id", "sender-session-id", strings.Repeat("x", protocol.AgentMessageMaxChars+1))
	if !strings.Contains(oversize, "32769") || !strings.Contains(oversize, "32768") {
		t.Fatalf("oversize detail names neither the ask nor the limit: %q", oversize)
	}
	if detail := refusal(t, "sender-session-id", "sender-session-id", "note to self"); !strings.Contains(detail, "yourself") {
		t.Fatalf("self-message detail = %q", detail)
	}

	if resp := callAgentMsg(t, d, "target-session-id", "sender-session-id", "same words"); resp.AgentMsgResult.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("first send = %+v", resp.AgentMsgResult)
	}
	repeat := refusal(t, "target-session-id", "sender-session-id", "same words")
	if !strings.Contains(repeat, "already sent") {
		t.Fatalf("duplicate detail = %q", repeat)
	}
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("a refused message was typed anyway: %q", prompts)
	}
}

func TestAgentMessageGuardVerdictNamesTheLimitAndTheAsk(t *testing.T) {
	if verdict := agentMessageGuardVerdict(agentmailbox.PeerGuardCounts{FromSenderInWindow: 2}); verdict != "" {
		t.Fatalf("a healthy exchange was refused: %q", verdict)
	}

	rate := agentMessageGuardVerdict(agentmailbox.PeerGuardCounts{FromSenderInWindow: agentMessageRateLimit})
	if !strings.Contains(rate, "8") || !strings.Contains(rate, "30s") {
		t.Fatalf("rate verdict = %q", rate)
	}
	full := agentMessageGuardVerdict(agentmailbox.PeerGuardCounts{UnreadForRecipient: agentMessageQueueCap})
	if !strings.Contains(full, "50") {
		t.Fatalf("queue-cap verdict = %q", full)
	}
	both := agentMessageGuardVerdict(agentmailbox.PeerGuardCounts{
		DuplicateFromSender: true, FromSenderInWindow: agentMessageRateLimit,
	})
	if !strings.Contains(both, "already sent") {
		t.Fatalf("verdict = %q", both)
	}
}

func TestSeedQueuedAgentMailboxItemsRestoresTheDrainAfterRestart(t *testing.T) {
	d, _ := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	if _, err := d.store.EnqueuePeerMessage(agentmailbox.PeerMessage{
		ID: "queued-across-restart", SenderSessionID: "sender-session-id",
		Body: "still owed", CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, "target-session-id"); err != nil {
		t.Fatal(err)
	}

	if d.hasQueuedAgentMailboxItems("target-session-id") {
		t.Fatal("a fresh daemon should not remember a message it never accepted")
	}
	d.seedQueuedAgentMailboxItems()
	if !d.hasQueuedAgentMailboxItems("target-session-id") {
		t.Fatal("seeding did not restore the queued target")
	}
}

// Live verification on 2026-08-10 found the cap sitting at the unix frame limit, where an
// oversize message closed the connection and reached the sender as a bare "EOF".
func TestAgentMsgSizeRefusalsSurviveTheSocket(t *testing.T) {
	useFreeWSPort(t)
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	c := client.New(sockPath)

	result, err := c.AgentMsg("target-session-id", "sender-session-id", strings.Repeat("x", protocol.AgentMessageMaxChars+1))
	if err != nil {
		t.Fatalf("a message one character over the cap did not come back as a refusal: %v", err)
	}
	if result.Status != protocol.AgentMsgStatusRefused || !strings.Contains(result.Detail, "32768") {
		t.Fatalf("result = %+v", result)
	}

}

func TestOversizeSocketFrameIsAnsweredNotDropped(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(func() { _ = d.store.Close() })

	caller, served := net.Pipe()
	defer caller.Close()
	go d.handleConnection(served)
	go io.WriteString(caller, `{"cmd":"agent_msg","content":"`+strings.Repeat("x", maxInitialSocketFrameBytes)+`"`)

	var resp protocol.Response
	if err := json.NewDecoder(caller).Decode(&resp); err != nil {
		t.Fatalf("the daemon said nothing about a frame it could not read: %v", err)
	}
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), strconv.Itoa(maxInitialSocketFrameBytes)) {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAgentMailboxRetryStaleCallbackLeavesItsReplacementArmed(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		drained := make(chan int, 1)
		d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
		if err := d.writeSessionPTY("target-session-id", []byte("a draft"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		callAgentMsg(t, d, "target-session-id", "sender-session-id", "the migration landed")
		d.agentMailboxMu.Lock()
		stale := d.agentMailboxRetries["target-session-id"]
		d.agentMailboxMu.Unlock()
		if stale == nil {
			t.Fatal("the held delivery armed no retry")
		}

		d.scheduleAgentMailboxDrain("target-session-id", sessionInputQuietWindow)
		d.agentMailboxMu.Lock()
		replacement := d.agentMailboxRetries["target-session-id"]
		d.agentMailboxMu.Unlock()
		if replacement == nil || replacement == stale {
			t.Fatalf("re-arming kept the old entry: %p", replacement)
		}

		d.fireAgentMailboxRetry("target-session-id", stale)
		d.agentMailboxMu.Lock()
		current := d.agentMailboxRetries["target-session-id"]
		d.agentMailboxMu.Unlock()
		if current != replacement {
			t.Fatalf("a stale callback evicted the replacement: %p", current)
		}
		if len(drained) != 0 || len(doorbell.pasted()) != 0 {
			t.Fatalf("a stale callback drained: pasted=%q", doorbell.pasted())
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if len(drained) != 1 || <-drained != 1 {
			t.Fatalf("the replacement did not drain once: pasted=%q", doorbell.pasted())
		}
	})
}

func TestAgentMailboxRetryRefusesToArmAfterStop(t *testing.T) {
	d, _ := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		d.scheduleAgentMailboxDrain("target-session-id", sessionInputQuietWindow)
		close(d.done)
		d.stopAgentMailboxRetries()
		d.scheduleAgentMailboxDrain("target-session-id", sessionInputQuietWindow)
		d.agentMailboxMu.Lock()
		armed := len(d.agentMailboxRetries)
		d.agentMailboxMu.Unlock()
		if armed != 0 {
			t.Fatalf("%d retries armed after stop", armed)
		}
		scheduled := make(chan string, 1)
		d.agentMailboxDrainScheduledHook = func(sessionID string) { scheduled <- sessionID }
		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if len(scheduled) != 0 {
			t.Fatal("a retry fired after stop")
		}
	})
}

func TestHandleAgentMsgHeldOffByTypingLandsAfterTheQuietWindow(t *testing.T) {
	d, doorbell := newAgentMsgDaemon(t)
	addCharacterizationSession(t, d, "sender-session-id", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "target-session-id", protocol.SessionAgentClaude, protocol.SessionStateWaitingInput)
	synctest.Test(t, func(t *testing.T) {
		drained := make(chan int, 1)
		d.agentMailboxDrainHook = func(_ string, delivered int) { drained <- delivered }
		if err := d.writeSessionPTY("target-session-id", []byte("a draft"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}

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
		if prompts := doorbell.pasted(); len(prompts) != 1 || !strings.Contains(prompts[0], "attn agent inbox "+result.MessageID) {
			t.Fatalf("doorbells after the window = %q", doorbell.pasted())
		}
	})
}
