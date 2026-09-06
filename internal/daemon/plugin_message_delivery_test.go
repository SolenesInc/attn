package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

func TestValidatePluginDriverCapabilities_AcceptsMessageDelivery(t *testing.T) {
	capabilities, err := validatePluginDriverCapabilities(map[string]bool{"message_delivery": true})
	if err != nil {
		t.Fatalf("validatePluginDriverCapabilities error=%v, want nil", err)
	}
	if !capabilities["message_delivery"] {
		t.Fatalf("capabilities=%+v, want message_delivery true", capabilities)
	}
}

func TestSessionInput_DeliversViaPluginAndWaitsForTakenReceipt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var inputRecorded bool
	backend.onInput = func(string, []byte) { inputRecorded = true }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-doorbell",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-doorbell", "pi-plugin", "run-doorbell") {
		t.Fatal("failed to begin plugin run")
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		if request.Method != "driver.deliver_message" {
			t.Errorf("method=%q, want driver.deliver_message", request.Method)
			return
		}
		var params pluginDeliverMessageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("decode deliver_message params: %v", err)
			return
		}
		if params.SessionID != "pi-doorbell" || params.RunID != "run-doorbell" ||
			params.InputID != "plugin-test/delivered" || params.Text != "ping from the chief" {
			t.Errorf("deliver_message params=%+v, want session/run/input/text match", params)
		}
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: true})
	}()

	delivery := maintenanceSessionInput("plugin-test", "delivered", "pi-doorbell", "ping from the chief", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("session input error=%v, want nil", attempt.err)
	}
	<-requestDone
	if got := d.sessionInputs().lookup(delivery.sessionID, delivery.id).stage; got != sessionInputPlaced {
		t.Fatalf("plugin acceptance produced stage %v, want Placed until the agent takes it", got)
	}
	stale := sendPluginMethodResponse(t, client, 2, "session.report_input_taken", pluginReportInputTakenParams{
		SessionID: "pi-doorbell", RunID: "run-stale", Seq: 1, InputID: delivery.id.String(),
	})
	if stale.Error == nil {
		t.Fatal("input-taken report from a stale run was accepted")
	}
	if got := d.sessionInputs().lookup(delivery.sessionID, delivery.id).stage; got != sessionInputPlaced {
		t.Fatalf("stale report produced stage %v, want Placed", got)
	}
	sendPluginMethod(t, client, 3, "session.report_state", pluginReportStateParams{
		SessionID: "pi-doorbell", RunID: "run-doorbell", Seq: 2, State: protocol.StateWorking,
	})
	sendPluginMethod(t, client, 4, "session.report_input_taken", pluginReportInputTakenParams{
		SessionID: "pi-doorbell", RunID: "run-doorbell", Seq: 1, InputID: delivery.id.String(),
	})
	if got := d.sessionInputs().lookup(delivery.sessionID, delivery.id).stage; got != sessionInputTaken {
		t.Fatalf("input-taken report produced stage %v, want Taken", got)
	}
	requestAt := protocol.Deref(d.store.Get("pi-doorbell").LastModelRequestAt)
	sendPluginMethod(t, client, 5, "session.report_input_taken", pluginReportInputTakenParams{
		SessionID: "pi-doorbell", RunID: "run-doorbell", Seq: 1, InputID: delivery.id.String(),
	})
	if got := protocol.Deref(d.store.Get("pi-doorbell").LastModelRequestAt); got != requestAt {
		t.Fatalf("duplicate report moved request clock from %s to %s", requestAt, got)
	}

	if inputRecorded {
		t.Fatal("session input wrote to the PTY for a message_delivery driver, want in-band delivery only")
	}
}

func TestSessionInput_KeepsPTYPasteWithoutMessageDeliveryCapability(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var recordedInput []byte
	backend.onInput = func(_ string, data []byte) { recordedInput = append(recordedInput, data...) }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-no-delivery",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-no-delivery", "pi-plugin", "run-no-delivery") {
		t.Fatal("failed to begin plugin run")
	}

	delivery := maintenanceSessionInput("plugin-test", "pty-fallback", "pi-no-delivery", "ping from the chief", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("session input error=%v, want nil", attempt.err)
	}
	if len(recordedInput) == 0 {
		t.Fatal("session input did not write to the PTY, want bracketed-paste fallback")
	}
	if got := string(recordedInput); len(got) < len(sessionInputPasteStart) || got[:len(sessionInputPasteStart)] != sessionInputPasteStart {
		t.Fatalf("input=%q, want bracketed-paste prefix", got)
	}
}

func TestSessionInput_DeliverMessageFailureSurfacesErrorWithoutPTYFallback(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	var inputRecorded bool
	backend.onInput = func(string, []byte) { inputRecorded = true }
	d.ptyBackend = backend

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-delivery-fails",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-delivery-fails", "pi-plugin", "run-fails") {
		t.Fatal("failed to begin plugin run")
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		request := decodeJSONRPCMessage(t, client)
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: false})
	}()

	delivery := maintenanceSessionInput("plugin-test", "failure", "pi-delivery-fails", "ping from the chief", sessionInputAtTurnBoundary)
	attempt := d.sessionInputs().try(context.Background(), delivery)
	<-requestDone
	if attempt.err == nil {
		t.Fatal("session input error=nil, want deliver_message ok=false to surface as an error")
	}
	if inputRecorded {
		t.Fatal("session input fell back to the PTY after a message_delivery failure, want no fallback")
	}
}

func TestSessionInput_UserBytesProceedWhilePluginPlacementIsInFlight(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	userWritten := make(chan struct{}, 1)
	d.ptyBackend = &fakeSpawnBackend{onInput: func(string, []byte) { userWritten <- struct{}{} }}

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-user-wins", Label: "pi", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-user-wins", "pi-plugin", "run-user-wins") {
		t.Fatal("failed to begin plugin run")
	}

	requestSeen := make(chan struct{})
	go func() {
		request := decodeJSONRPCMessage(t, client)
		close(requestSeen)
		<-userWritten
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: true})
	}()

	delivery := maintenanceSessionInput("plugin-test", "slow-placement", "pi-user-wins", "maintenance", sessionInputAtTurnBoundary)
	result := make(chan sessionInputAttempt, 1)
	go func() { result <- d.sessionInputs().try(context.Background(), delivery) }()
	<-requestSeen
	if err := d.writeSessionPTY("pi-user-wins", []byte("the user's live bytes"), "user"); err != nil {
		t.Fatalf("user input: %v", err)
	}
	if attempt := <-result; attempt.err != nil || attempt.stage != sessionInputPlaced {
		t.Fatalf("plugin placement = %+v, want Placed", attempt)
	}
}

func TestPluginClassifyStop_RejectsUnauthorizedRun(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-auth",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-auth", "pi-plugin", "run-real") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 30, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-auth",
		RunID:         "run-wrong",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error == nil {
		t.Fatal("attn.classify_stop with the wrong run_id succeeded, want ownership error")
	}
}

func TestPluginClassifyStop_RejectsEmptyAssistantText(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-empty",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-empty", "pi-plugin", "run-empty") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 31, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-empty",
		RunID:         "run-empty",
		AssistantText: "   ",
	})
	if response.Error == nil {
		t.Fatal("attn.classify_stop with blank assistant_text succeeded, want validation error")
	}
}

func TestPluginClassifyStop_HappyPathReturnsClassifierVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = NewFakeClassifier(protocol.StateWaitingInput)
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-happy",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-happy", "pi-plugin", "run-happy") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 32, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-happy",
		RunID:         "run-happy",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error != nil {
		t.Fatalf("attn.classify_stop error=%#v, want nil", response.Error)
	}
	var result pluginClassifyStopResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode classify_stop result: %v", err)
	}
	if result.Verdict != protocol.StateWaitingInput {
		t.Fatalf("verdict=%q, want waiting_input", result.Verdict)
	}
}

func TestPluginClassifyStop_ClassifierErrorYieldsUnknownVerdict(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.classifier = &errorClassifier{state: protocol.StateUnknown, err: errors.New("classifier execution failed")}
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-classify-error",
		Label:          "pi",
		Agent:          "pi",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-classify-error", "pi-plugin", "run-error") {
		t.Fatal("failed to begin plugin run")
	}

	response := sendPluginMethodResponse(t, client, 33, "attn.classify_stop", pluginClassifyStopParams{
		SessionID:     "pi-classify-error",
		RunID:         "run-error",
		AssistantText: "Should I proceed with the migration?",
	})
	if response.Error != nil {
		t.Fatalf("attn.classify_stop error=%#v, want a successful unknown verdict, not a JSON-RPC failure", response.Error)
	}
	var result pluginClassifyStopResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode classify_stop result: %v", err)
	}
	if result.Verdict != protocol.StateUnknown {
		t.Fatalf("verdict=%q, want unknown after classifier error", result.Verdict)
	}
}

func TestSessionInput_UserTurnViaPluginTitlesTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	runner := installSessionTitleRunner(t, d)

	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"message_delivery": true})

	directory := t.TempDir()
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "pi-titled",
		Label:          defaultSessionLabel(directory, "pi-titled"),
		Agent:          "pi",
		Directory:      directory,
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("pi-titled", "pi-plugin", "run-titled") {
		t.Fatal("failed to begin plugin run")
	}

	var conversations []string
	d.sessionTitleExec = func(ctx context.Context, session *protocol.Session, conversation string) (string, error) {
		conversations = append(conversations, conversation)
		return "Retry queue investigation", nil
	}
	settled := make(chan jobs.State, 4)
	runner.OnChange(func(jobID string) {
		if job, _ := runner.Get(jobID); job != nil && (job.State == jobs.StateDone || job.State == jobs.StateDead) {
			settled <- job.State
		}
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)

	go func() {
		request := decodeJSONRPCMessage(t, client)
		respondPluginRequest(t, client, request, pluginDeliverMessageResult{OK: true})
	}()
	delivery := userConversationSessionInput("steer", "pi-titled", "investigate the retry queue", sessionInputAtTurnBoundary)
	if attempt := d.sessionInputs().try(context.Background(), delivery); attempt.err != nil {
		t.Fatalf("session input error=%v, want nil", attempt.err)
	}

	if state := <-settled; state != jobs.StateDone {
		t.Fatalf("title job state = %s, want done", state)
	}
	if len(conversations) != 1 || !strings.Contains(conversations[0], "investigate the retry queue") {
		t.Fatalf("title conversations = %q, want one carrying the steered prompt", conversations)
	}
	if got := d.store.Get("pi-titled"); got == nil || got.Label != "Retry queue investigation" {
		t.Fatalf("session label = %+v, want %q", got, "Retry queue investigation")
	}
}
