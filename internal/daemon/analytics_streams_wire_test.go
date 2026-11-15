package daemon_test

import (
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAppActionsReachTheOutboxOnlyWhileAnalyticsIsOn(t *testing.T) {
	w := newWorld(t)
	app := w.App()

	sendAppAction(app, "shortcut", map[string]any{"id": "dock.asks"})
	recordPaletteCommand(app, "snooze-active-session")
	if got := streamRecords(t, app, analytics.StreamApp); len(got) != 0 {
		t.Fatalf("with analytics off the outbox holds app records %v", got)
	}

	setAnalyticsEnabled(app, "true")
	sendAppAction(app, "shortcut", map[string]any{"id": "dock.asks", "label": "Open asks"})
	sendAppAction(app, "queue_advance", map[string]any{"to": "session", "wait_ms": 42000})
	recordPaletteCommand(app, "snooze-active-session")

	got := streamRecords(t, app, analytics.StreamApp)
	want := []string{
		`palette_command {"id":"snooze-active-session"}`,
		`queue_advance {"to":"session","wait_ms":42000}`,
		`shortcut {"id":"dock.asks","label":{"s":9}}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("app records, newest first:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	seqs := streamSeqs(t, app, analytics.StreamApp)
	if seqs[0] != 3 || seqs[1] != 2 || seqs[2] != 1 {
		t.Errorf("app seqs, newest first = %v, want 3 2 1", seqs)
	}
}

func TestSocketRequestsWriteNoCLIRecordsAndRefusalsNameTheirReason(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	setAnalyticsEnabled(app, "true")

	socketCommand(t, w, `{"cmd":"query","caller_id":"`+w.Terminal(session)+`"}`)
	socketCommand(t, w, `{"cmd":"no_such_command"}`)
	var refused protocol.Response
	if err := json.Unmarshal(socketReply(t, w, `{"cmd":"seed_show","seed_id":"s-zzzzzz"}`), &refused); err != nil {
		t.Fatal(err)
	}
	if refused.Ok || protocol.Deref(refused.Refusal) != protocol.RefusalNotFound {
		t.Fatalf("seed_show of a missing seed replied ok=%v refusal=%v error=%q, want a not_found refusal", refused.Ok, refused.Refusal, protocol.Deref(refused.Error))
	}
	if got := streamRecords(t, app, analytics.StreamCLI); len(got) != 0 {
		t.Fatalf("socket requests wrote cli records %v; the CLI writes one per command", got)
	}
}

func TestCLIRecordsCarryTheSameHashedSubjectAsSessionFacts(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		agent := w.bubbleClaude(t, app, "shop")
		synctest.Wait()

		var subject string
		for _, record := range mirroredRecords(t, app, "session.state.changed") {
			subject = record.Subject
		}
		if !strings.HasPrefix(subject, "h:") {
			t.Fatalf("session facts carry subject %q, want a hashed session", subject)
		}

		for _, caller := range []string{agent.self, ""} {
			socketCommand(t, w, `{"cmd":"record_analytics","stream":"cli","name":"seed show","props":{"ok":true,"exit_code":0},"caller_id":"`+caller+`"}`)
			got := streamOutbox(t, app, analytics.StreamCLI)[0]
			want, wantProps := "", `{"caller":"human","exit_code":0,"ok":true}`
			if caller != "" {
				want, wantProps = subject, `{"caller":"agent","exit_code":0,"harness":"claude","ok":true}`
			}
			if got.Name != "seed show" || got.Subject != want || string(got.Props) != wantProps {
				t.Errorf("record from caller %q = %s %s subject %q, want seed show %s subject %q", caller, got.Name, got.Props, got.Subject, wantProps, want)
			}
		}
	})
}

const sampleInterval = 20 * time.Minute

func TestOneResourceSampleEveryTwentyMinutesWhileAnalyticsIsOn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		w.advance(sampleInterval + time.Second)
		if got := streamRecords(t, app, analytics.StreamSample); len(got) != 0 {
			t.Fatalf("with analytics off the outbox holds samples %v", got)
		}

		setAnalyticsEnabled(app, "true")
		w.advance(sampleInterval)
		listed := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 10, Stream: protocol.Ptr(string(analytics.StreamSample))})
		if len(listed.Records) != 1 {
			t.Fatalf("after twenty minutes with analytics on the outbox holds %d samples, want 1", len(listed.Records))
		}
		var record analytics.Record
		if err := json.Unmarshal([]byte(listed.Records[0].Record), &record); err != nil {
			t.Fatal(err)
		}
		var sample map[string]any
		if err := json.Unmarshal(record.Props, &sample); err != nil {
			t.Fatalf("sample props %s: %v", record.Props, err)
		}
		if record.Name != "process" {
			t.Errorf("sample name = %q, want process", record.Name)
		}
		for _, field := range []string{"goroutines", "resident_bytes", "open_fds", "heap_alloc", "database_bytes", "bus_retained_facts", "connected_clients"} {
			if measured, _ := sample[field].(float64); measured <= 0 {
				t.Errorf("sample %s = %v, want a positive measurement (props %s)", field, sample[field], record.Props)
			}
		}
		if runtime.GOOS == "linux" {
			if measured, _ := sample["c_heap_in_use_bytes"].(float64); measured <= 0 {
				t.Errorf("sample c_heap_in_use_bytes = %v, want a positive measurement on Linux (props %s)", sample["c_heap_in_use_bytes"], record.Props)
			}
		}
		if _, ok := sample["cpu_time_ms"]; !ok {
			t.Errorf("sample has no cpu_time_ms (props %s)", record.Props)
		}
	})
}

type processSampleProps struct {
	Workload struct {
		Sessions   int `json:"sessions"`
		Agents     int `json:"agents"`
		Terminals  int `json:"terminals"`
		PanesDrawn int `json:"panes_drawn"`
		Desktops   int `json:"desktops"`
		Seeds      int `json:"seeds"`
	} `json:"workload"`
	Processes []struct {
		Role           string `json:"role"`
		Shell          string `json:"shell"`
		Count          int    `json:"count"`
		ResidentBytes  uint64 `json:"resident_bytes"`
		FootprintBytes uint64 `json:"footprint_bytes"`
	} `json:"processes"`
}

func newestProcessSample(t *testing.T, app *testworld.Peer) processSampleProps {
	t.Helper()
	records := streamOutbox(t, app, analytics.StreamSample)
	for _, record := range records {
		if record.Name != "process" {
			continue
		}
		var props processSampleProps
		if err := json.Unmarshal(record.Props, &props); err != nil {
			t.Fatalf("process sample props %s: %v", record.Props, err)
		}
		return props
	}
	t.Fatalf("the outbox holds no process sample among %d sample records", len(records))
	return processSampleProps{}
}

func (p processSampleProps) appShell() string {
	for _, process := range p.Processes {
		if process.Role == "app" {
			return process.Shell
		}
	}
	return ""
}

func (p processSampleProps) roleCount(role string) int {
	for _, process := range p.Processes {
		if process.Role == role {
			return process.Count
		}
	}
	return 0
}

func TestASampleCarriesItsWorkloadAndTheMemoryOfTheConnectedAppByRole(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		shell := w.Spawn(app, fakeagent.Harness("shell"), w.Path("notes"))
		w.bubbleClaude(t, app, "shop")
		synctest.Wait()
		terminal := protocol.TerminalID(w.Terminal(shell))
		testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == terminal })
		plantSeed(t, w, "Count me")

		w.advance(sampleInterval)
		sample := newestProcessSample(t, app)

		workload := sample.Workload
		if workload.Sessions != 2 || workload.Agents != 1 || workload.PanesDrawn != 1 || workload.Desktops < 1 || workload.Seeds != 1 {
			t.Errorf("sample workload = %+v, want 2 sessions, 1 agent, 1 pane drawn, a desktop and 1 seed", workload)
		}
		for _, process := range sample.Processes {
			if process.Count < 1 || process.ResidentBytes == 0 {
				t.Errorf("sample of role %s = %+v, want a measured process", process.Role, process)
			}
		}
		if sample.roleCount("daemon") != 1 || sample.roleCount("app") != 1 {
			t.Errorf("sample processes = %+v, want the daemon and the connected app", sample.Processes)
		}
		if shell := sample.appShell(); shell != "tauri" {
			t.Errorf("the Tauri app's sample entry has shell %q, want tauri", shell)
		}

		observer := w.Connect(helloWithToken(protocol.Ptr(config.ClientToken())), nil)
		app.Close()
		synctest.Wait()
		w.advance(sampleInterval)
		after := newestProcessSample(t, observer)
		if after.roleCount("app") != 0 || after.Workload.PanesDrawn != 0 {
			t.Errorf("after the app left the sample has processes %+v and %d panes drawn, want neither", after.Processes, after.Workload.PanesDrawn)
		}
	})
}

func TestASampleNamesTheNativeAppsShell(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		observer := w.App()
		setAnalyticsEnabled(observer, "true")
		observer.Close()
		hello := helloWithToken(protocol.Ptr(config.ClientToken()))
		hello.ClientKind = protocol.ClientKindNativeApp
		hello.AppPid = protocol.Ptr(os.Getpid())
		native := w.Connect(hello, nil)
		testworld.Await[protocol.InitialStateMessage](native, protocol.EventInitialState, nil)
		synctest.Wait()

		w.advance(sampleInterval)
		sample := newestProcessSample(t, native)

		if sample.roleCount("app") != 1 || sample.appShell() != "native" {
			t.Errorf("sample processes = %+v, want one app entry with shell native", sample.Processes)
		}
	})
}

func sendAppAction(app *testworld.Peer, name string, props map[string]any) {
	app.Send(protocol.RecordAnalyticsMessage{Cmd: protocol.CmdRecordAnalytics, Stream: string(analytics.StreamApp), Name: name, Props: props})
}

func recordPaletteCommand(app *testworld.Peer, commandID string) {
	app.T.Helper()
	id := uuid.NewString()
	result := testworld.Request(app, protocol.RecordCommandUsageMessage{
		Cmd: protocol.CmdRecordCommandUsage, RequestID: id, ProfileID: app.SelectedProfile(), CommandID: commandID,
	}, protocol.EventRecordCommandUsageResult, func(r protocol.RecordCommandUsageResultMessage) bool { return r.RequestID == id })
	if !result.Success {
		app.T.Fatalf("record command usage: %s", protocol.Deref(result.Error))
	}
}

func streamOutbox(t *testing.T, app *testworld.Peer, stream analytics.Stream) []analytics.Record {
	t.Helper()
	listed := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 100, Stream: protocol.Ptr(string(stream))})
	records := make([]analytics.Record, 0, len(listed.Records))
	for _, row := range listed.Records {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		records = append(records, record)
	}
	return records
}

func streamRecords(t *testing.T, app *testworld.Peer, stream analytics.Stream) []string {
	t.Helper()
	var lines []string
	for _, record := range streamOutbox(t, app, stream) {
		lines = append(lines, record.Name+" "+string(record.Props))
	}
	return lines
}

func streamSeqs(t *testing.T, app *testworld.Peer, stream analytics.Stream) []int64 {
	t.Helper()
	var seqs []int64
	for _, record := range streamOutbox(t, app, stream) {
		seqs = append(seqs, record.Seq)
	}
	return seqs
}

func socketCommand(t *testing.T, w *world, frame string) {
	t.Helper()
	socketReply(t, w, frame)
}

func socketReply(t *testing.T, w *world, frame string) []byte {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, frame+"\n"); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read %s until the daemon closed: %v", frame, err)
	}
	return reply
}
