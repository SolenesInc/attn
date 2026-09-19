package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func recordFilesEdited(t *testing.T, d *Daemon, sessionID string, paths []string) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleFilesEdited(serverConn, &protocol.FilesEditedMessage{
		Cmd:   protocol.CmdFilesEdited,
		ID:    sessionID,
		Paths: paths,
	})

	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Ok {
		t.Fatalf("response = %+v", resp)
	}
}

func TestFilesEditedRecordsMarkdownAgainstTheSession(t *testing.T) {
	d := newDaemonForTest(t)

	recordFilesEdited(t, d, "session-1", []string{"/repo/docs/plan.md"})

	files := d.store.GetRecentFiles(10, "")
	if len(files) != 1 {
		t.Fatalf("files = %+v, want the edited file recorded", files)
	}
	if files[0].Path != "/repo/docs/plan.md" || files[0].Source != store.FileActivitySourceEdited {
		t.Errorf("entry = %+v, want /repo/docs/plan.md recorded as edited", files[0])
	}
	if files[0].SessionID == nil || *files[0].SessionID != "session-1" {
		t.Errorf("session_id = %v, want session-1", files[0].SessionID)
	}
}

func TestFilesEditedDropsWhatTheOpenerCannotShow(t *testing.T) {
	d := newDaemonForTest(t)

	recordFilesEdited(t, d, "session-1", []string{
		"/repo/main.go",
		"docs/relative.md",
		"",
		"/repo/kept.MARKDOWN",
	})

	files := d.store.GetRecentFiles(10, "")
	if len(files) != 1 || files[0].Path != "/repo/kept.MARKDOWN" {
		t.Fatalf("files = %+v, want only the absolute markdown path", files)
	}
}
