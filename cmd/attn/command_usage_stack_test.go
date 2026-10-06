package main_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCommandUsageSurvivesDaemonProcessRestart(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	profile := app.SelectedProfile()
	id := uuid.NewString()
	record := testworld.Request(app, protocol.RecordCommandUsageMessage{
		Cmd: protocol.CmdRecordCommandUsage, RequestID: id, ProfileID: profile, CommandID: "settings",
	}, protocol.EventRecordCommandUsageResult, func(r protocol.RecordCommandUsageResultMessage) bool { return r.RequestID == id })
	if !record.Success {
		t.Fatalf("record: %+v", record)
	}
	s.Stop()
	s.Start()
	app = s.AppOn(profile)
	id = uuid.NewString()
	history := testworld.Request(app, protocol.GetCommandUsageMessage{
		Cmd: protocol.CmdGetCommandUsage, RequestID: id, ProfileID: profile,
	}, protocol.EventGetCommandUsageResult, func(r protocol.GetCommandUsageResultMessage) bool { return r.RequestID == id })
	if !history.Success || len(history.Entries) != 1 || history.Entries[0].CommandID != "settings" || history.Entries[0].Score <= 0 || history.Entries[0].LastUsedAt == "" {
		t.Fatalf("persistent history: %+v", history)
	}
}
