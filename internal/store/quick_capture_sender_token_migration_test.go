package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestUpgradeDropsSenderTokensAndKeepsCaptureRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793658171589938)
	if err != nil {
		t.Fatal(err)
	}
	const token = "fake-paired-sender-token"
	saved := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, SenderToken: protocol.Ptr(token), CaptureID: "8d1f5a4e-7c1b-4c1e-9b8a-2f5f7c0d9e11", Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("target"))}, Content: "Quotes \" <tags> & ünïcode\n☃  ", AttachmentIds: []string{}}
	submittedBeforeUpgrade, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO quick_captures(profile_id,id,inbox_item_id,submission,mailbox_kind,mailbox_session_id,body,created_at) VALUES('home',?,'inbox-item',?,'session','target',?,?)`, saved.CaptureID, string(submittedBeforeUpgrade), saved.Content, time.Now().UTC().Format(sortableTimeFormat)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var submission string
	if err := s.db.QueryRow(`SELECT submission FROM quick_captures WHERE id=?`, saved.CaptureID).Scan(&submission); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(submission, token) {
		t.Fatalf("upgraded submission still holds the sender token: %s", submission)
	}
	for _, retry := range []*string{protocol.Ptr(token), protocol.Ptr("a-later-token"), nil} {
		again := saved
		again.SenderToken = retry
		if replay, err := s.QuickCaptureReplay("home", again); !replay || err != nil {
			t.Fatalf("retrying a capture sent before the upgrade with token %v: replay=%v %v", retry, replay, err)
		}
	}
	changed := saved
	changed.Content = "Other content"
	if _, err := s.QuickCaptureReplay("home", changed); err == nil || !strings.Contains(err.Error(), "different payload") {
		t.Fatalf("reusing a capture id from before the upgrade for other content: %v", err)
	}
}
