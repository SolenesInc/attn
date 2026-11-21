package daemon_test

import (
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

func filesOfAttachment(t *testing.T, w *world, id string) []string {
	t.Helper()
	var found []string
	root := filepath.Join(filepath.Dir(w.Socket), "attachments")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), id) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestAnUploadRacingAnAsksWithdrawalCannotBringItsFileBack(t *testing.T) {
	for _, race := range []struct {
		name, point    string
		firstChunkSent bool
	}{
		{"after the upload read its row", pausepoint.AttachmentRowRead, true},
		{"after the upload checked its ask and before its first row", pausepoint.AttachmentOwnerChecked, false},
	} {
		t.Run(race.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.TrustedApp()
				setSetting(t, app, "asks_enabled", "true")
				asker := w.bubbleClaude(t, app, "shop")
				open := func() protocol.Ask {
					ask, err := asker.cli.AskOpen(protocol.SessionID(asker.id), "", cacheAsk, "", w.Path("shop"))
					if err != nil {
						t.Fatal(err)
					}
					return *ask
				}
				ownedBy := func(ask protocol.Ask) protocol.AttachmentOwner {
					return protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: ask.ID}
				}
				chunk := func(owner protocol.AttachmentOwner, id string, offset int, data string, final bool) protocol.AttachmentPutMessage {
					return protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: owner, AttachmentID: id, Name: "draft.txt", Offset: offset, DataBase64: base64.StdEncoding.EncodeToString([]byte(data)), Final: final}
				}

				answered, withdrawn := open(), open()
				sent := answerFile(t, app, ownedBy(answered), "sent.txt", []byte("sent"), true)
				if reply := answerWithFiles(app, answered.ID, "Numbers attached.", sent); !reply.Success {
					t.Fatalf("answering failed: %s", protocol.Deref(reply.Error))
				}

				draft := uuid.NewString()
				if race.firstChunkSent {
					if _, err := app.Attachment(chunk(ownedBy(withdrawn), draft, 0, "dra", false)); err != nil {
						t.Fatal(err)
					}
				}
				uploadHeld, cleanupHeld := w.PauseAt(race.point), w.PauseAt(pausepoint.AskFilesDiscarding)
				offset, data := 0, "draft"
				if race.firstChunkSent {
					offset, data = 3, "ft"
				}
				uploaded := make(chan error, 1)
				go func() {
					_, err := app.Attachment(chunk(ownedBy(withdrawn), draft, offset, data, true))
					uploaded <- err
				}()
				uploadHeld.Await()
				closed := make(chan error, 1)
				go func() { closed <- asker.cli.AskClose(protocol.SessionID(asker.id), withdrawn.ID, "") }()
				cleanupHeld.Await()
				uploadHeld.Release()
				if err := <-uploaded; err == nil || !strings.Contains(err.Error(), "already answered or withdrawn") {
					t.Fatalf("the upload that raced the withdrawal = %v, want it refused as the ask's", err)
				}
				cleanupHeld.Release()
				if err := <-closed; err != nil {
					t.Fatalf("withdrawing the ask: %v", err)
				}

				check := func(when string) {
					t.Helper()
					if left := filesOfAttachment(t, w, draft); len(left) != 0 {
						t.Errorf("%s, the withdrawn ask's file is on disk at %v", when, left)
					}
					if left := filesOfAttachment(t, w, sent); len(left) != 1 {
						t.Errorf("%s, the answered ask's sent file is at %v, want one", when, left)
					}
					if _, err := app.Attachment(chunk(ownedBy(withdrawn), draft, 0, "draft", true)); err == nil {
						t.Errorf("%s, a retry of the upload was accepted", when)
					}
				}
				check("after the withdrawal")
				w.restart()
				check("after the daemon restarted")
			})
		})
	}
}
