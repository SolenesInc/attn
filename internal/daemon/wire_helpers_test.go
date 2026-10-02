package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func (w *world) finishStartupWork() {
	w.T.Helper()
	if w.bubbled {
		w.advance(0)
		return
	}
	watcher := w.App()
	for !everyTaskSettled(watcher) {
		testworld.Await[protocol.TasksChangedMessage](watcher, protocol.EventTasksChanged, nil)
	}
}

func everyTaskSettled(p *testworld.Peer) bool {
	p.T.Helper()
	requestID := uuid.NewString()
	listed := testworld.Request(p, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
	for _, task := range listed.Tasks {
		if task.State != "done" && task.State != "dead" {
			return false
		}
	}
	return true
}

func setSetting(t *testing.T, app *testworld.Peer, key, value string) {
	t.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(result.Success) {
		t.Fatalf("set %s = %s: %s", key, value, protocol.Deref(result.Error))
	}
}

func writeCrewCharter(t *testing.T, w *world, member string) {
	t.Helper()
	home := filepath.Join(w.Dir, crew.HomesDirName, member)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, crew.CharterFileName), []byte("# "+member+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
