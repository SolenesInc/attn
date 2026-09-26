package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func delegateForNotify(t *testing.T, d *Daemon, agent string) (chiefID, agentID string, inputs func(string) []string) {
	t.Helper()
	backend := &fakeSpawnBackend{}
	var mu sync.Mutex
	rec := map[string][]string{}
	backend.onInput = func(id string, data []byte) {
		mu.Lock()
		rec[id] = append(rec[id], string(data))
		mu.Unlock()
	}
	_, chiefID, _ = setupDelegationSource(t, d, backend)
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, chiefID); err != nil {
		t.Fatalf("set chief role: %v", err)
	}
	setSessionAgent(t, d, chiefID, protocol.SessionAgentClaude)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(chiefID),
		Brief:           protocol.Ptr("Migrate the store to X"),
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	bindLegacyTicket(t, d, result.SessionID, chiefID)
	inputs = func(id string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rec[id]...)
	}
	return chiefID, result.SessionID, inputs
}

func setSessionAgent(t *testing.T, d *Daemon, sessionID string, agent protocol.SessionAgent) {
	t.Helper()
	s := d.store.Get(sessionID)
	if s == nil {
		t.Fatalf("setSessionAgent: session %s not found", sessionID)
	}
	s.Agent = agent
	d.store.Add(s)
}

func wasNudged(inputs []string) bool {
	for _, in := range inputs {
		if strings.Contains(in, agentMailboxDoorbellText) {
			return true
		}
	}
	return false
}

func TestQueuedNudgeTypesDoorbellOnlyOnceTheSessionGoesIdle(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		_, agentID, inputs := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		d.applyState(sessionStateChange{
			sessionID: agentID,
			state:     protocol.StateWorking,
			cause:     resolverObservation{},
		})

		commentOnTicket(t, d, ticketID, "take a look")
		settledNudgeDeadline(t, d, agentID)
		time.Sleep(defaultNudgeCountdownWindow)
		synctest.Wait()
		if wasNudged(inputs(agentID)) {
			t.Fatal("deferred nudge typed into the session while it was working")
		}

		d.applyState(sessionStateChange{
			sessionID: agentID,
			state:     protocol.StateIdle,
			cause:     resolverObservation{},
		})
		synctest.Wait()
		if !wasNudged(inputs(agentID)) {
			t.Fatal("queued nudge did not wake when the session became idle")
		}
	})
}

func commentOnTicket(t *testing.T, d *Daemon, ticketID, comment string) {
	t.Helper()
	if resp := callTicketComment(t, d, store.TicketAuthorYou, ticketID, comment); !resp.Ok {
		t.Fatalf("comment on %s: %v", ticketID, protocol.Deref(resp.Error))
	}
}

func TestTicketNudgesActiveChiefAcrossRuntimes(t *testing.T) {
	for _, runtime := range []protocol.SessionAgent{protocol.SessionAgentCodex, protocol.SessionAgentClaude} {
		t.Run(string(runtime), func(t *testing.T) {
			d := newBubbleDaemon(t)
			synctest.Test(t, func(t *testing.T) {
				stopDaemonBackground(t, d)
				chiefID, agentID, inputs := delegateForNotify(t, d, "codex")
				setSessionAgent(t, d, chiefID, runtime)
				d.store.UpdateState(chiefID, protocol.StateWorking)
				d.setSelectedSession(agentID)

				callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "done, please review")
				time.Sleep(time.Until(settledNudgeDeadline(t, d, chiefID)) + time.Second)
				synctest.Wait()
				if wasNudged(inputs(chiefID)) {
					t.Fatalf("working %s chief received terminal input", runtime)
				}
				if unread, err := d.store.HasUnreadAgentMailboxItems(chiefID); err != nil || !unread {
					t.Fatalf("working %s chief lost durable activity: unread=%v err=%v", runtime, unread, err)
				}

				d.applyState(sessionStateChange{
					sessionID: chiefID,
					state:     protocol.StateIdle,
					cause:     resolverObservation{},
				})
				synctest.Wait()
				if !wasNudged(inputs(chiefID)) {
					t.Fatalf("idle %s chief was not woken for queued activity", runtime)
				}
				if wasNudged(inputs(agentID)) {
					t.Fatal("the reporting agent was nudged about its own status change")
				}
			})
		})
	}
}

func TestChiefTicketContinuityAcrossRoleTransfer(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		chiefA, agentID, inputs := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)

		now := string(protocol.TimestampNow())
		chiefB := "chief-b"
		d.store.Add(&protocol.Session{
			ID: chiefB, Label: "replacement chief", Agent: protocol.SessionAgentCodex,
			Directory: "/tmp/chief-b", WorkspaceID: "workspace-chief-b",
			State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		d.store.UpdateState(chiefA, protocol.StateIdle)
		d.store.UpdateState(agentID, protocol.StateIdle)

		callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateNeedsInput), "need a decision")
		first := callTicketInbox(t, d, chiefA)
		if len(first) != 1 || len(first[0].Events) != 1 ||
			first[0].Events[0].ToStatus == nil || *first[0].Events[0].ToStatus != protocol.TicketStatusBlocked {
			t.Fatalf("chief A first inbox = %+v, want only the blocked report", first)
		}
		nudgesA := nudgeCount(inputs(chiefA))

		if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, chiefB); err != nil {
			t.Fatalf("transfer chief role: %v", err)
		}
		d.retargetChiefTicketDelivery(chiefA, chiefB)

		callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "ready now")
		if deadline := currentNudgeDeadline(d, chiefA); !deadline.IsZero() {
			t.Fatalf("retired chief still has a countdown armed for %s", deadline)
		}
		time.Sleep(2 * d.ticketBundleWindow())
		synctest.Wait()
		if !wasNudged(inputs(chiefB)) {
			t.Fatal("replacement chief was not nudged about unread chief-owned ticket activity")
		}
		if got := nudgeCount(inputs(chiefA)); got != nudgesA {
			t.Fatalf("retired chief was nudged about a ticket it delegated as chief: %d -> %d", nudgesA, got)
		}

		second := callTicketInbox(t, d, chiefB)
		if len(second) != 1 || second[0].TicketID != ticketID || len(second[0].Events) != 1 {
			t.Fatalf("chief B inbox = %+v, want exactly one post-cursor event for %s", second, ticketID)
		}
		event := second[0].Events[0]
		if event.ToStatus == nil || *event.ToStatus != protocol.TicketStatusInReview || event.Author != agentID {
			t.Fatalf("chief B event = %+v, want agent's in-review report", event)
		}
		if again := callTicketInbox(t, d, chiefB); len(again) != 0 {
			t.Fatalf("chief B second inbox = %+v, want no duplicate activity", again)
		}
	})
}

func TestChiefRoleAndExplicitSubscriptionDeliverOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	chiefID, agentID, _ := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	if resp := callTicketSubscribe(t, d, chiefID, ticketID); !resp.Ok {
		t.Fatalf("subscribe response = %+v", resp)
	}

	callSetTicketStatus(t, d, agentID, string(protocol.DispatchWorkStateReadyForReview), "ready")
	bundles := callTicketInbox(t, d, chiefID)
	if len(bundles) != 1 || len(bundles[0].Events) != 1 {
		t.Fatalf("overlapping role/subscriber inbox = %+v, want one event", bundles)
	}
	if again := callTicketInbox(t, d, chiefID); len(again) != 0 {
		t.Fatalf("overlapping role/subscriber second inbox = %+v, want empty", again)
	}
}

func TestTicketActivityWakesSleepingMemberAndDoorbellsOnIdleWithoutPromptHook(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	doorbell := &recordingDoorbell{}
	backend.onInput = doorbell.backend().onInput
	var initialPrompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		body, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Fatalf("read initial prompt: %v", err)
		}
		initialPrompt = string(body)
	}

	identity := store.TicketMemberIdentity("trellis")
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{ID: "sleeping-thread", Title: "Sleeping thread"}, "you", now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.AddTicketSubscription(identity, "sleeping-thread", now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AddTicketComment("sleeping-thread", "you", "new activity", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	d.notifyTicketObservers("sleeping-thread")

	member := memberByID(t, crewList(t, d), "trellis")
	sessionID := protocol.Deref(member.BindingSession)
	if sessionID == "" {
		t.Fatal("ticket activity did not wake Trellis")
	}
	if initialPrompt != crewWakePrompt {
		t.Fatalf("wake initial prompt = %q, want the ordinary post-priming greeting", initialPrompt)
	}
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("ticket nudge landed before priming completed: %q", prompts)
	}
	if currentNudgeTimer(d, sessionID) == nil {
		t.Fatal("ticket activity did not schedule independently of the prompt hook")
	}
	fireNudgeNow(t, d, sessionID)
	if prompts := doorbell.pasted(); len(prompts) != 0 {
		t.Fatalf("countdown spliced into priming: %q", prompts)
	}
	decorated := d.sessionForBroadcast(d.store.Get(sessionID))
	if decorated == nil || !protocol.Deref(decorated.TicketUnread) {
		t.Fatalf("woken member session = %+v, want unread indicator", decorated)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries(sessionID)
	if err != nil || len(unread) != 1 || unread[0].Item.Prompt != ticketNudgePrompt {
		t.Fatalf("durable ticket mailbox after countdown = %+v, %v", unread, err)
	}

	drains := observeAgentMailboxDrains(t, d)
	if !d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     protocol.StateIdle,
		cause:     liveSignal{},
	}) {
		t.Fatal("idle state did not apply")
	}
	if delivered := drains.next(); delivered != 1 {
		t.Fatalf("idle drain delivered %d doorbells, want 1", delivered)
	}
	if !wasNudged(doorbell.pasted()) {
		t.Fatalf("woken member was not nudged on idle without a hook: %q", doorbell.pasted())
	}
}

func nudgeCount(inputs []string) int {
	n := 0
	for _, in := range inputs {
		if strings.Contains(in, agentMailboxDoorbellText) {
			n++
		}
	}
	return n
}
