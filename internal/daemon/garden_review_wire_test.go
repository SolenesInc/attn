package daemon

import (
	"encoding/json"
	"net"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestGardenReviewShowUsesTheSameSnapshotOnIPCAndWebSocket(t *testing.T) {
	d := newGardenDaemon(t)
	run := garden.ReviewRun{
		ID: "r-wire-test", CandidateIDs: []string{"s-wire"},
		Recipe: garden.ReviewRecipe{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
		Status: garden.ReviewRunStatusComplete, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	item := garden.ReviewItem{
		ID: garden.ReviewItemID(run.ID, "s-wire"), RunID: run.ID, SeedID: "s-wire",
		SeedRev: 7, EvidenceVersion: "evidence-1", Title: "Check the wire", Body: "Verify it.",
		Evidence: []garden.ReviewEvidence{{Label: "Lifecycle", Text: "Old enough."}},
		Actions:  []string{"handover", "park", "harvest", "wither"},
		Status:   garden.ReviewItemStatusReady, Resolution: garden.ReviewResolutionUnresolved,
		Recommendation: "harvest", Explanation: "The stated verification is complete.",
	}
	if err := d.createGardenReview(run, []garden.ReviewItem{item}); err != nil {
		t.Fatalf("createGardenReview: %v", err)
	}

	ipc := gardenCall(t, func(conn net.Conn) {
		d.handleSeedReviewShow(conn, &protocol.SeedReviewShowMessage{
			Cmd: protocol.CmdSeedReviewShow, ReviewID: protocol.Ptr(run.ID),
		})
	})
	if !ipc.Ok || ipc.SeedReviewResult == nil {
		t.Fatalf("IPC response = %+v", ipc)
	}

	client := newInternalWSClient()
	d.handleSeedReviewShowWS(client, &protocol.SeedReviewShowMessage{
		Cmd: protocol.CmdSeedReviewShow, RequestID: protocol.Ptr("request-1"), ReviewID: protocol.Ptr(run.ID),
	})
	message := <-client.send
	var reply protocol.SeedReviewResultMessage
	if err := json.Unmarshal(message.payload, &reply); err != nil {
		t.Fatalf("decode WebSocket response: %v", err)
	}
	if !reply.Success || reply.RequestID != "request-1" || reply.Operation != "show" || reply.Review == nil {
		t.Fatalf("WebSocket response = %+v", reply)
	}
	got := reply.Review.Items[0].Actions
	want := ipc.SeedReviewResult.Review.Items[0].Actions
	if !slices.Equal(got, want) {
		t.Fatalf("WebSocket actions = %v, IPC actions = %v", got, want)
	}
}

func TestGardenReviewShowCountsCandidatesWithoutStartingARun(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	oldUnheldGrowingSeed(t, d, now)

	response := gardenCall(t, func(conn net.Conn) {
		d.handleSeedReviewShow(conn, &protocol.SeedReviewShowMessage{Cmd: protocol.CmdSeedReviewShow})
	})
	if !response.Ok || response.SeedReviewResult == nil {
		t.Fatalf("show response = %+v", response)
	}
	if response.SeedReviewResult.Review != nil || response.SeedReviewResult.CandidateCount != 1 {
		t.Fatalf("overview = %+v, want one candidate and no started run", response.SeedReviewResult)
	}
}

func TestGardenReviewCommandsParseToTheirTypedMessages(t *testing.T) {
	tests := []struct {
		raw  string
		cmd  string
		want any
	}{
		{`{"cmd":"seed_review_start"}`, protocol.CmdSeedReviewStart, &protocol.SeedReviewStartMessage{}},
		{`{"cmd":"seed_review_show"}`, protocol.CmdSeedReviewShow, &protocol.SeedReviewShowMessage{}},
		{`{"cmd":"seed_review_cancel","review_id":"r-1"}`, protocol.CmdSeedReviewCancel, &protocol.SeedReviewCancelMessage{}},
		{`{"cmd":"seed_review_retry","review_id":"r-1","seed_id":"s-1"}`, protocol.CmdSeedReviewRetry, &protocol.SeedReviewRetryMessage{}},
	}
	for _, test := range tests {
		cmd, message, err := protocol.ParseMessage([]byte(test.raw))
		if err != nil {
			t.Fatalf("ParseMessage(%s): %v", test.cmd, err)
		}
		if cmd != test.cmd || reflect.TypeOf(message) != reflect.TypeOf(test.want) {
			t.Fatalf("ParseMessage(%s) = %s %T, want %s %T", test.cmd, cmd, message, test.cmd, test.want)
		}
	}
}
