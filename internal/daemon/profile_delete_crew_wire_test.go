package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDeletingCrewFilesLetsTheUserDeleteTheEmptyProfile(t *testing.T) {
	w := newWorld(t)
	writeCrewHomeFile(t, w, "keel", crew.CharterFileName, "# Keel\n\nA crew member.\n")
	w.restart()
	app := w.App()
	original := app.SelectedProfile()
	createProfile(app, "Keep")
	remove := func() protocol.ProfileActionResultMessage {
		var revision int
		for _, profile := range w.AppOn(original).Initial.Profiles {
			if profile.ID == original {
				revision = profile.Revision
			}
		}
		return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: "delete-crew-profile", ProfileID: original, ExpectedRevision: revision}, "delete-crew-profile")
	}
	if refused := remove(); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "1 crew members") {
		t.Fatalf("profile with crew: %+v", refused)
	}
	if err := os.RemoveAll(filepath.Join(w.Dir, crew.HomesDirName, "keel")); err != nil {
		t.Fatal(err)
	}
	if deleted := remove(); !deleted.Success {
		t.Fatalf("profile after user file cleanup: %+v", deleted)
	}
}
