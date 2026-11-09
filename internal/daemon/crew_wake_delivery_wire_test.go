package daemon_test

import (
	"testing"
	"testing/synctest"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const wokenAnswer = "Answer: Postgres (the recommended choice)"

func answerAsk(t *testing.T, app *testworld.Peer, askID string) {
	t.Helper()
	requestID := uuid.NewString()
	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: askID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
	if !answered.Success {
		t.Fatalf("answering %s failed: %s", askID, protocol.Deref(answered.Error))
	}
}

func memberAsks(t *testing.T, w *world, day protocol.SessionID, seed string) (answer func(*testworld.Peer)) {
	t.Helper()
	asked, err := w.Client().AskOpen(day, seed, cacheAsk, "", w.Path("trellis"))
	if err != nil {
		t.Fatalf("the member asks: %v", err)
	}
	return func(app *testworld.Peer) { answerAsk(t, app, asked.ID) }
}

func memberTendsASeed(t *testing.T, w *world, day protocol.SessionID) string {
	t.Helper()
	cli := w.Client()
	seed := plantSeedAs(t, cli, string(day), "pick a store")
	gardenNudgeMove(t, cli, string(day), seed, "tend")
	return seed
}

func TestTheUsersWordsThatWakeAMemberAreTypedOnlyOnceItsHarnessHasStarted(t *testing.T) {
	const capture = "look at the flaky test"
	for _, item := range []struct {
		name, words string
		prepare     func(t *testing.T, w *world, day protocol.SessionID) (deliver func(*testworld.Peer))
	}{
		{"an answer to the member's ask", wokenAnswer, func(t *testing.T, w *world, day protocol.SessionID) func(*testworld.Peer) {
			return memberAsks(t, w, day, "")
		}},
		{"an answer to the member's ask about a seed", wokenAnswer, func(t *testing.T, w *world, day protocol.SessionID) func(*testworld.Peer) {
			return memberAsks(t, w, day, memberTendsASeed(t, w, day))
		}},
		{"a quick capture", capture, func(t *testing.T, w *world, _ protocol.SessionID) func(*testworld.Peer) {
			return func(*testworld.Peer) {
				sendQuickCapture(t, w.TrustedApp(), protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr("trellis")}, capture)
			}
		}},
		{"a change to a seed's authorization", authorizationTyped, func(t *testing.T, w *world, day protocol.SessionID) func(*testworld.Peer) {
			seed := memberTendsASeed(t, w, day)
			return func(app *testworld.Peer) {
				testworld.Request(app, protocol.SeedAuthorizationMessage{
					Cmd: protocol.CmdSeedAuthorization, RequestID: protocol.Ptr("set"), SeedID: seed, Text: protocol.Ptr("You may land on main."),
				}, protocol.EventSeedAuthorizationResult, func(m protocol.SeedAuthorizationResultMessage) bool { return m.RequestID == "set" })
			}
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, day := crewDayInBubble(t, w, map[string]string{"asks_enabled": "true", "crew.heartbeat_enabled": "false", "crew.away_close_enabled": "false"})
				cli := w.Client()
				deliver := item.prepare(t, w, protocol.SessionID(day.id))
				crewHandoff(t, cli, day.id, "Asleep until the user writes.", false)
				synctest.Wait()

				deliver(app)
				synctest.Wait()
				woken := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
				if woken == "" || string(woken) == day.id {
					t.Fatalf("the user's words woke %q, want a new session", woken)
				}
				term := w.terminal(string(woken))
				term.WorkerReportsItsProcessRunning()
				synctest.Wait()
				if inputs := term.Inputs(); len(inputs) != 0 {
					t.Fatalf("attn typed into a harness that had not started: %+v", inputs)
				}
				if booting := queriedSession(t, cli, string(woken)); booting.State != protocol.SessionStateLaunching {
					t.Fatalf("a harness that has not started shows %s, want launching", booting.State)
				}

				agent := w.bootBubbleClaude(t, string(woken))
				if got := agent.promptsContaining(item.words); got != 1 {
					t.Fatalf("the started agent took %q, want %q once", agent.prompts(), item.words)
				}
			})
		})
	}
}
