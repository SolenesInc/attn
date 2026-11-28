package daemon_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const bugReportView = `# Bug report

:::question-file screenshot
Show me the broken screen.
:::
`

var savesAttachment = regexp.MustCompile(`attn agent attachment (\S+) --out <path>`)

// giveViewFile uploads data as the file name for the view whose files owner
// owns, and answers its question screenshot with it.
func giveViewFile(t *testing.T, trusted, app *testworld.Peer, tile viewTileOf, owner protocol.AttachmentOwner, name string, data []byte) string {
	t.Helper()
	id := answerFile(t, trusted, owner, name, data, true)
	answerView(t, app, tile, protocol.ViewAnswer{Part: protocol.Ptr("screenshot"), Attachments: []string{id}})
	return id
}

// screenshotLine is the one line the responses at path give for the question
// screenshot.
func screenshotLine(t *testing.T, path string) string {
	t.Helper()
	answers, _ := readResponses(t, path)["answers"].(map[string]any)
	lines, _ := answers["screenshot"].([]any)
	if len(lines) != 1 {
		t.Fatalf("the responses answer the screenshot with %v, want one line", answers["screenshot"])
	}
	line, _ := lines[0].(string)
	return line
}

func saveAttachment(t *testing.T, w *world, terminal, id, out string) ([]byte, error) {
	t.Helper()
	var result any
	if err := w.Client().WithCaller(protocol.TerminalID(terminal)).RunCommand("agent attachment", "", map[string]any{"attachment": id, "out": out}, &result); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

func TestAFileGivenToAViewOutlivesARestartAndReachesTheAgentAsTheLineThatSavesIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "views_enabled", "true")
		asker := w.bubbleClaude(t, app, "shop")
		folder := w.Path("bug")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{"view.md": bugReportView})
		if problems, err := cli.OpenMarkdown(view, protocol.SessionID(asker.id)); err != nil || len(problems) != 0 {
			t.Fatalf("opening the view answered problems %+v, error %v; want none", problems, err)
		}
		opened := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil })
		tile := viewTileOf{desktop: opened.DesktopID, tile: opened.TileID}
		owner := opened.Answers.Owner
		app.TypeLine(asker.id, "look at my bug")
		asker.term.Heartbeat("busy", "Claude Code")
		w.advance(time.Second)
		asker.reply("Opened the view for you. <!-- attn:state=waiting_input -->")

		trusted := w.TrustedApp()
		nowhere := protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindView, ID: owner.ID + ".gone"}
		if _, err := trusted.Attachment(protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: nowhere, AttachmentID: uuid.NewString(), Name: "x.png", DataBase64: base64.StdEncoding.EncodeToString([]byte("x")), Final: true}); err == nil {
			t.Fatal("an upload for a view no tile shows was accepted")
		}
		half := answerFile(t, trusted, owner, "still uploading.png", []byte("half"), false)
		requestID := uuid.NewString()
		if refused := testworld.Request(app, protocol.ViewAnswerMessage{Cmd: protocol.CmdViewAnswer, RequestID: requestID, DesktopID: tile.desktop, TileID: tile.tile, Answer: protocol.ViewAnswer{Part: protocol.Ptr("screenshot"), Attachments: []string{half}}},
			protocol.EventViewAnswerResult, func(r protocol.ViewAnswerResultMessage) bool { return r.RequestID == requestID }); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "is staged") {
			t.Errorf("answering with a file still uploading = %+v, want a refusal", refused)
		}

		first := giveViewFile(t, trusted, app, tile, owner, "wrong window.png", []byte("the wrong window"))
		screenshot := quickCapturePNG(t)
		chosen := giveViewFile(t, trusted, app, tile, owner, "broken screen.png", screenshot)
		w.restart()
		app = w.App()
		asker.cli = w.Client()
		w.advance(0)
		restored := awaitViewTile(app, view, func(m protocol.DesktopTileContentMessage) bool { return m.Answers != nil })
		if len(restored.Answers.Drafts) != 1 || len(restored.Answers.Drafts[0].Attachments) != 1 || restored.Answers.Drafts[0].Attachments[0] != chosen ||
			len(restored.Answers.Files) != 1 || restored.Answers.Files[0].Name != "broken screen.png" {
			t.Fatalf("after a restart the tile holds %+v, want the screenshot the user chose last", restored.Answers)
		}
		if _, err := saveAttachment(t, w, asker.self, first, filepath.Join(w.Path("shop"), "first.png")); err == nil {
			t.Errorf("the file the user replaced can still be saved, want it forgotten")
		}

		sent := sendView(t, app, tile)
		if !sent.Told {
			t.Fatalf("sending answered %+v, want the agent told", sent)
		}
		line := screenshotLine(t, sent.Responses)
		t.Logf("the responses give the screenshot as %q", line)
		if !strings.HasPrefix(line, `File "broken screen.png" (image/png, `) {
			t.Errorf("the screenshot's line is %q, want it to name the file", line)
		}
		found := savesAttachment.FindStringSubmatch(line)
		if found == nil {
			t.Fatalf("the screenshot's line %q gives no command that saves it", line)
		}
		saved, err := saveAttachment(t, w, asker.self, found[1], filepath.Join(w.Path("shop"), "screen.png"))
		if err != nil || !bytes.Equal(saved, screenshot) {
			t.Errorf("the agent saved %d bytes through the line, %v; want the screenshot", len(saved), err)
		}
		if got := asker.promptsContaining("The user sent \"Bug report\": 1 answer.\nResponses: " + sent.Responses); got != 1 {
			t.Errorf("the summary was typed %d times, want once: %q", got, asker.prompts())
		}

		giveViewFile(t, w.TrustedApp(), app, tile, owner, "another.png", []byte("another"))
		if again, err := saveAttachment(t, w, asker.self, chosen, filepath.Join(w.Path("shop"), "again.png")); err != nil || !bytes.Equal(again, screenshot) {
			t.Errorf("after the user chose another file, the one sent saved %d bytes, %v; want it kept for the agent", len(again), err)
		}
	})
}
