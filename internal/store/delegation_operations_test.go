package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegationPullRequestReceiptPersistsBeforeCompletion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, claimed, err := s.ClaimDelegationOperation("request-pr", "operation-pr", "session-pr", "", "", `{"pull_request":"42"}`, now); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	want := protocol.DelegatePullRequestReceipt{Source: "42", URL: "https://github.com/owner/repo/pull/42", Number: 42, State: "open", BaseRepository: "github.com/owner/repo", HeadRepository: "github.com/fork/repo", HeadBranch: "feature", HeadSHA: "abc", LocalBranch: "feature"}
	saved, err := s.SaveDelegationPullRequestReceipt("operation-pr", want, now.Add(time.Second))
	if err != nil || saved.HeadSHA != "abc" {
		t.Fatalf("save receipt = %+v, %v", saved, err)
	}
	other := want
	other.HeadSHA = "new-live-head"
	saved, err = s.SaveDelegationPullRequestReceipt("operation-pr", other, now.Add(2*time.Second))
	if err != nil || saved.HeadSHA != "abc" {
		t.Fatalf("retry changed frozen receipt = %+v, %v", saved, err)
	}
	want.WorktreePath = "/tmp/repo--feature"
	want.VerifiedHead = "abc"
	want.Disposition = "created"
	if err := s.UpdateDelegationPullRequestReceipt("operation-pr", want, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	record, err := s.GetDelegationOperation("request-pr")
	if err != nil || record.Operation.PullRequest == nil || record.Operation.PullRequest.VerifiedHead != "abc" {
		t.Fatalf("record = %+v, %v", record, err)
	}
	var raw protocol.DelegatePullRequestReceipt
	if err := json.Unmarshal([]byte(record.ResolvedPR), &raw); err != nil || raw.WorktreePath != want.WorktreePath {
		t.Fatalf("raw receipt = %+v, %v", raw, err)
	}
}

func TestDelegationPullRequestReceiptMigrationUpgradesPreviousSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
		ALTER TABLE delegation_operations DROP COLUMN resolved_pr_json;
		DELETE FROM schema_migrations WHERE version >= 140;
	`); err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	var columns int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = 'resolved_pr_json'`).Scan(&columns); err != nil || columns != 1 {
		t.Fatalf("resolved_pr_json column: count=%d err=%v", columns, err)
	}
	if _, claimed, err := migrated.ClaimDelegationOperation("request-migrated-pr", "operation-migrated-pr", "session-migrated-pr", "", "", `{}`, time.Now()); err != nil || !claimed {
		t.Fatalf("claim after migration: claimed=%v err=%v", claimed, err)
	}
	want := protocol.DelegatePullRequestReceipt{Source: "7", URL: "https://github.com/owner/repo/pull/7", Number: 7, HeadSHA: "0123456789012345678901234567890123456789"}
	if _, err := migrated.SaveDelegationPullRequestReceipt("operation-migrated-pr", want, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err := migrated.GetDelegationOperation("operation-migrated-pr")
	if err != nil || record.Operation.PullRequest == nil || record.Operation.PullRequest.HeadSHA != want.HeadSHA {
		t.Fatalf("migrated receipt=%+v err=%v", record, err)
	}
}

func TestDelegationOperationReservesActiveTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	first, claimed, err := s.ClaimDelegationOperation("request-1", "operation-1", "session-1", "", "planned", `{"ticket_id":"planned"}`, now)
	if err != nil || !claimed {
		t.Fatalf("first claim = %+v, %v, %v", first, claimed, err)
	}
	if protocol.Deref(first.Operation.TicketID) != "planned" {
		t.Fatalf("reserved ticket = %q", protocol.Deref(first.Operation.TicketID))
	}
	_, _, err = s.ClaimDelegationOperation("request-2", "operation-2", "session-2", "", "planned", `{"ticket_id":"planned"}`, now)
	if !errors.Is(err, ErrTicketDelegationReserved) {
		t.Fatalf("second claim error = %v, want ErrTicketDelegationReserved", err)
	}
	if err := s.UpdateDelegationOperation("operation-1", protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), now); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.ClaimDelegationOperation("request-3", "operation-3", "session-3", "", "planned", `{"ticket_id":"planned"}`, now); err != nil || !claimed {
		t.Fatalf("claim after terminal operation = %v, %v", claimed, err)
	}
}
