package daemon_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAConversationReportedDuringLaunchSurvivesTheRunsImmediateExit(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := s.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	request := protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "launching", Agent: "snipe", Cwd: cwd,
		ProfileID: app.SelectedProfile(), Cols: 80, Rows: 24,
	}
	app.Send(request)
	var launch driverLaunch
	reply := driver.asked("driver.spawn", &launch)
	driver.launched()
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": launch.SessionID, "run_id": launch.RunID, "seq": 1,
		"metadata": map[string]string{"native_id": "reported-before-launch"}, "resume_session_id": "reported-before-launch",
	})
	driver.answer(reply, map[string]any{"argv": []string{"/bin/sh", "-c", "exit 7"}})
	if result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == request.ID }); !result.Success {
		t.Fatalf("launch failed: %s", protocol.Deref(result.Error))
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == request.ID })
	driver.mu.Lock()
	driver.holdLaunch = false
	driver.mu.Unlock()
	s.Spawn(s.App(), scriptedAgent, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = request.ID })
	resumed := driver.launched()
	if resumed.Method != "driver.resume" || resumed.ResumeSessionID != "reported-before-launch" {
		t.Fatalf("relaunch asked %s for %q, want driver.resume for reported-before-launch", resumed.Method, resumed.ResumeSessionID)
	}
	t.Logf("daemon launch=%s exited=7 resumed=%s", request.ID, resumed.ResumeSessionID)
}

func TestAStaleLaunchCompletionLeavesTheNewerReloadsReportsQueued(t *testing.T) {
	s := testworld.NewStack(t)
	complete := s.PauseAt(pausepoint.AgentLaunchReady)
	s.Start()
	app := s.App()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := s.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: "launching", Agent: "snipe", Cwd: cwd, ProfileID: app.SelectedProfile(), Cols: 80, Rows: 24}
	app.Send(request)
	first := driver.launched()
	complete.Await()
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	reloader := s.App()
	reloader.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: request.ID, Cols: 80, Rows: 24})
	var second driverLaunch
	reply := driver.asked("driver.resume", &second)
	driver.launched()
	report := func(seq int, native string) {
		t.Helper()
		driver.mustReport("session.report_metadata", map[string]any{
			"session_id": second.SessionID, "run_id": second.RunID, "seq": seq,
			"metadata": map[string]string{"native_id": native}, "resume_session_id": native,
		})
	}
	report(1, "first-report-of-reload")
	complete.Release()
	if result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == request.ID }); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	report(2, "latest-report-of-reload")
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	if result := testworld.Await(reloader, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == request.ID }); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	if closed := driver.closed(); closed.RunID != first.RunID {
		t.Fatalf("reload closed run %s, want %s", closed.RunID, first.RunID)
	}
	driver.mu.Lock()
	driver.holdLaunch = false
	driver.mu.Unlock()
	stopDriven(reloader, driver, string(request.ID))
	s.Spawn(s.App(), scriptedAgent, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = request.ID })
	if resumed := driver.launched(); resumed.ResumeSessionID != "latest-report-of-reload" {
		t.Fatalf("relaunch resumes %q, want latest-report-of-reload", resumed.ResumeSessionID)
	}
}

func TestAPendingMetadataReplyDoesNotBlockTheDriversReloadResponse(t *testing.T) {
	s := testworld.NewStack(t)
	applying := s.PauseAt(pausepoint.ConversationReportReady)
	s.Start()
	app := s.App()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"resume": true, "state_reporting": true, "launch_instructions": true})
	awaitDriverAvailable(app, "snipe")
	session := s.Spawn(app, scriptedAgent, s.Path("shop"))
	first := driver.launched()
	reported := make(chan error, 1)
	go func() {
		reported <- driver.report("session.report_metadata", map[string]any{
			"session_id": first.SessionID, "run_id": first.RunID, "seq": 1,
			"metadata": map[string]string{"native_id": "pending"}, "resume_session_id": "pending",
		})
	}()
	applying.Await()
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24})
	reply := driver.asked("driver.resume", nil)
	driver.launched()
	applying.Release()
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	if result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session }); !result.Success {
		t.Fatalf("reload failed while a metadata reply waited: %s", protocol.Deref(result.Error))
	}
	if err := <-reported; err == nil {
		t.Fatal("the replaced runtime's unanswered metadata was acknowledged as bound")
	}
}
