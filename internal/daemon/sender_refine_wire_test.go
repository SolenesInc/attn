package daemon_test

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
)

func senderRefine(t *testing.T, w *world, token, text string, recording time.Duration) net.Conn {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	msg := protocol.SenderRefineMessage{Cmd: protocol.CmdSenderRefine, SenderToken: token, RawText: text,
		RecordingDurationMs: int(recording.Milliseconds()), Format: protocol.DictationTextFormatMarkdown}
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatal(err)
	}
	return conn
}

func senderRefineReply(t *testing.T, conn net.Conn) protocol.Response {
	t.Helper()
	var reply protocol.Response
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		t.Fatalf("read sender_refine reply: %v", err)
	}
	return reply
}

func TestAPairedSenderRefinesUnderItsOwnProfile(t *testing.T) {
	w := newDictationWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	paired := pairSenderInApp(t, cli, app, "Lens")

	conn := senderRefine(t, w, paired.Token, "um deploy to staging", 4*time.Second)
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, `{"transcript":"um deploy to staging"}`) || !strings.Contains(task.Prompt, "Markdown document") {
		t.Fatalf("the sender's refinement ran with prompt %q, want the transcript for a Markdown document", task.Prompt)
	}
	task.Answer("Deploy to staging.\n")
	reply := senderRefineReply(t, conn)
	got := reply.SenderRefineResult
	if !reply.Ok || got == nil || !got.Success || protocol.Deref(got.RefinedText) != "Deploy to staging." || got.ProfileID != side.ID || got.LimitMs != nil {
		t.Fatalf("sender_refine replied %+v (result %+v), want the refinement under profile %s", reply, got, side.ID)
	}

	failing := senderRefine(t, w, paired.Token, "hello", time.Second)
	w.HeadlessTask().Fail("stream ended unexpectedly")
	if got := senderRefineReply(t, failing).SenderRefineResult; got == nil || got.Success || !strings.Contains(protocol.Deref(got.Error), "claude: headless agent process failed") {
		t.Fatalf("a failing refinement answered %+v", got)
	}
}

func TestSenderRefinementRefusesWhatAttnSendRefuses(t *testing.T) {
	w := newDictationWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	paired := pairSenderInApp(t, cli, app, "Lens")
	for _, tc := range []struct {
		name, token, text string
		recording         time.Duration
		want              string
	}{
		{"no token", "", "hello", 0, "attn send pair --name"},
		{"unknown token", "not-a-paired-token", "hello", 0, "matches no paired sender"},
		{"no text", paired.Token, "  ", 0, "needs raw_text"},
		{"negative duration", paired.Token, "hello", -time.Millisecond, "must not be negative, got -1"},
	} {
		reply := senderRefineReply(t, senderRefine(t, w, tc.token, tc.text, tc.recording))
		if reply.Ok || !strings.Contains(protocol.Deref(reply.Error), tc.want) || reply.SenderRefineResult != nil {
			t.Errorf("%s: replied %+v, want an error naming %q", tc.name, reply, tc.want)
		}
	}

	if err := cli.UnpairSender("", "Lens"); err != nil {
		t.Fatal(err)
	}
	if reply := senderRefineReply(t, senderRefine(t, w, paired.Token, "hello", 0)); reply.Ok || !strings.Contains(protocol.Deref(reply.Error), "matches no paired sender") {
		t.Fatalf("an unpaired sender's refinement replied %+v", reply)
	}
}

func TestSenderRefinementExpiresAtAttnsLimitAndStopsWhenTheSenderLeaves(t *testing.T) {
	w := newDictationWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	paired := pairSenderInApp(t, cli, app, "Lens")

	expiring := senderRefine(t, w, paired.Token, "deploy to staging", 40*time.Second)
	w.awaitTimer(daemon.DictationRefineDeadlineTimer)
	slow := w.HeadlessTask()
	w.advance(25 * time.Second)
	got := senderRefineReply(t, expiring).SenderRefineResult
	if got == nil || got.Success || protocol.Deref(got.LimitMs) != 25000 || !strings.Contains(protocol.Deref(got.Error), "within 25 s for this 40 s recording") {
		t.Fatalf("an expired refinement answered %+v", got)
	}
	slow.AwaitExited()
	slow.Answer("Deploy to staging.")

	leaving := senderRefine(t, w, paired.Token, "last thought", time.Second)
	abandoned := w.HeadlessTask()
	leaving.Close()
	abandoned.AwaitExited()
	abandoned.Answer("Last thought.")

	next := senderRefine(t, w, paired.Token, "next thought", time.Second)
	w.HeadlessTask().Answer("Next thought.")
	if got := senderRefineReply(t, next).SenderRefineResult; got == nil || protocol.Deref(got.RefinedText) != "Next thought." {
		t.Fatalf("a refinement after the sender left answered %+v", got)
	}
}
