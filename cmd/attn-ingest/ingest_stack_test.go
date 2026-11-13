package main_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/testworld"
)

func TestMain(m *testing.M) {
	os.Exit(testworld.Main(m))
}

type client struct {
	t         *testing.T
	url       string
	token     string
	installID string
}

func (c client) do(method, path string, body []byte) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, reply
}

func (c client) register() client {
	c.t.Helper()
	status, body := c.do(http.MethodPost, "/v1/installs", nil)
	if status != http.StatusOK {
		c.t.Fatalf("register: %d %s", status, body)
	}
	var registration analytics.Registration
	decode(c.t, body, &registration)
	if !strings.HasPrefix(registration.InstallID, "i-") || registration.Token == "" {
		c.t.Fatalf("register returned %s", body)
	}
	c.token = registration.Token
	c.installID = registration.InstallID
	return c
}

func (c client) send(batch analytics.Batch) (int, []byte) {
	c.t.Helper()
	var body bytes.Buffer
	if err := analytics.WriteBatch(&body, batch); err != nil {
		c.t.Fatal(err)
	}
	return c.do(http.MethodPost, "/v1/events", body.Bytes())
}

func (c client) storedEvents(clickHouse testworld.ClickHouse) string {
	c.t.Helper()
	return clickHouse.Query(c.t, fmt.Sprintf("SELECT count() FROM events WHERE install_id = '%s'", c.installID))
}

func decode(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func busRecords(first, count int64) []analytics.Record {
	at := time.Date(2026, 10, 19, 14, 2, 11, 0, time.UTC)
	records := make([]analytics.Record, count)
	for i := range records {
		seq := first + int64(i)
		records[i] = analytics.Record{
			Stream:  analytics.StreamBus,
			Seq:     seq,
			Name:    "pr.updated",
			Subject: fmt.Sprintf("h:%012x", seq),
			At:      at.Add(time.Duration(seq) * time.Millisecond),
		}
	}
	return records
}

func TestAnInstallSendsRetriesHitsTheMinuteLimitAndIsDeleted(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()
	build := analytics.CurrentBuild()

	records := append(busRecords(1, 99), analytics.Record{
		Stream: analytics.StreamApp,
		Seq:    1,
		Name:   "a.name.no.build.has.sent.yet",
		At:     time.Date(2026, 10, 19, 14, 3, 0, 0, time.UTC),
		Props:  json.RawMessage(`{"kind":"palette","duration_ms":18,"path":{"s":24}}`),
	})
	batch := analytics.Batch{Build: build, Records: records}

	status, body := install.send(batch)
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"accepted":100,"duplicates":0}` {
		t.Fatalf("first send: %d %s", status, body)
	}
	status, body = install.send(batch)
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"accepted":0,"duplicates":100}` {
		t.Fatalf("retried send: %d %s", status, body)
	}
	if streams := clickHouse.Query(t, "SELECT stream, count() FROM events GROUP BY stream ORDER BY stream"); streams != "app\t1\nbus\t99" {
		t.Fatalf("after a retry ClickHouse holds %q, want 99 bus and 1 app event", streams)
	}
	stored := clickHouse.Query(t, "SELECT build_commit, build_channel, length(ip_hash), props FROM events WHERE stream = 'app'")
	want := fmt.Sprintf("%s\t%s\t32\t{\"kind\":\"palette\",\"duration_ms\":18,\"path\":{\"s\":24}}", build.Commit, build.Channel)
	if stored != want {
		t.Fatalf("stored app row %q, want %q", stored, want)
	}

	status, body = install.send(analytics.Batch{Build: build, Records: busRecords(100, 10_000)})
	if status != http.StatusTooManyRequests {
		t.Fatalf("a batch past the minute limit: %d %s", status, body)
	}
	var refusal analytics.Refusal
	decode(t, body, &refusal)
	if refusal.Limit != "events_per_install_per_minute" || refusal.Value != 10_000 || refusal.Observed != 10_200 || refusal.RetryAfterS < 1 || refusal.RetryAfterS > 60 {
		t.Fatalf("refusal %s, want events_per_install_per_minute at 10000 with 10200 observed and a retry within a minute", body)
	}
	if events := install.storedEvents(clickHouse); events != "100" {
		t.Fatalf("a refused batch stored rows: ClickHouse holds %s events", events)
	}

	if status, body := install.do(http.MethodDelete, "/v1/installs/me", nil); status != http.StatusNoContent {
		t.Fatalf("DELETE /v1/installs/me: %d %s", status, body)
	}
	if rows := clickHouse.Query(t, "SELECT count() FROM events"); rows != "0" {
		t.Fatalf("after delete ClickHouse holds %s events", rows)
	}
	if installs := clickHouse.Query(t, "SELECT count() FROM installs"); installs != "0" {
		t.Fatalf("after delete ClickHouse holds %s installs", installs)
	}
	if status, _ := install.send(batch); status != http.StatusUnauthorized {
		t.Fatalf("the deleted install's token still answers POST /v1/events with %d", status)
	}

	var sent bytes.Buffer
	if err := analytics.WriteBatch(&sent, batch); err != nil {
		t.Fatal(err)
	}
	_, metrics := install.do(http.MethodGet, "/metrics", nil)
	for _, line := range []string{
		"attn_ingest_events_accepted_total 100",
		"attn_ingest_events_duplicate_total 100",
		fmt.Sprintf("attn_ingest_batch_bytes_accepted_total %d", 2*sent.Len()),
		`attn_ingest_refusals_total{limit="events_per_install_per_minute"} 1`,
	} {
		if !bytes.Contains(metrics, []byte(line+"\n")) {
			t.Errorf("/metrics lacks %q:\n%s", line, metrics)
		}
	}
}

func paddedRecords(t *testing.T, count int64) []analytics.Record {
	t.Helper()
	pad, err := json.Marshal(map[string]string{"pad": strings.Repeat("a", 60_000)})
	if err != nil {
		t.Fatal(err)
	}
	records := busRecords(1, count)
	for i := range records {
		records[i].Props = pad
	}
	return records
}

func encodedBatch(t *testing.T, records []analytics.Record) ([]byte, int64) {
	t.Helper()
	var body bytes.Buffer
	if err := analytics.WriteBatch(&body, analytics.Batch{Build: analytics.CurrentBuild(), Records: records}); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.Copy(io.Discard, zr)
	if err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), plain
}

type bytesSender struct {
	t          *testing.T
	body       []byte
	plainBytes int64
	sent       int64
}

func (b *bytesSender) accept(install client) {
	b.t.Helper()
	if status, reply := install.do(http.MethodPost, "/v1/events", b.body); status != http.StatusOK {
		b.t.Fatalf("batch %d of %d uncompressed bytes: %d %s", b.sent+1, b.plainBytes, status, reply)
	}
	b.sent++
}

func (b *bytesSender) refused(install client, limit string, value, observed int64) {
	b.t.Helper()
	status, reply := install.do(http.MethodPost, "/v1/events", b.body)
	var refusal analytics.Refusal
	decode(b.t, reply, &refusal)
	if status != http.StatusTooManyRequests || refusal.Limit != limit || refusal.Value != value || refusal.Observed != observed || refusal.RetryAfterS < 1 || refusal.RetryAfterS > 86_400 {
		b.t.Fatalf("batch %d: %d %s, want %s at %d with %d observed and a retry within a day", b.sent+1, status, reply, limit, value, observed)
	}
	if !bytes.Contains(install.metrics(), []byte(fmt.Sprintf("attn_ingest_refusals_total{limit=%q} 1\n", limit))) {
		b.t.Fatalf("/metrics does not count the %s refusal:\n%s", limit, install.metrics())
	}
}

func (c client) metrics() []byte {
	c.t.Helper()
	_, body := c.do(http.MethodGet, "/metrics", nil)
	return body
}

func TestOneInstallStoresAtMost128MiBADayAndOthersAreUnaffected(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	anonymous := client{t: t, url: ingest.URL}
	body, plainBytes := encodedBatch(t, paddedRecords(t, 270))
	sender := &bytesSender{t: t, body: body, plainBytes: plainBytes}
	const budget = int64(128 << 20)

	full := anonymous.register()
	for range budget / plainBytes {
		sender.accept(full)
	}
	sender.refused(full, "bytes_per_install_per_day", budget, (budget/plainBytes+1)*plainBytes)

	fresh := anonymous.register()
	sender.accept(fresh)
	if events := fresh.storedEvents(clickHouse); events != "270" {
		t.Fatalf("a fresh install's first batch stored %s events, want 270", events)
	}
}

func TestAllInstallsTogetherStoreAtMostTheConfiguredBytesADay(t *testing.T) {
	const budget = int64(64 << 10)
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse, fmt.Sprintf("ATTN_INGEST_BYTES_PER_DAY=%d", budget))
	anonymous := client{t: t, url: ingest.URL}
	body, plainBytes := encodedBatch(t, busRecords(1, 100))
	sender := &bytesSender{t: t, body: body, plainBytes: plainBytes}

	for sender.sent < budget/plainBytes {
		sender.accept(anonymous.register())
	}
	late := anonymous.register()
	sender.refused(late, "bytes_per_day", budget, (budget/plainBytes+1)*plainBytes)
	if events := late.storedEvents(clickHouse); events != "0" {
		t.Fatalf("a batch past the budget for all installs stored %s events", events)
	}
}

func TestOneAddressRegistersAtMostTwentyInstallsAnHour(t *testing.T) {
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t))
	anonymous := client{t: t, url: ingest.URL}
	for range 20 {
		anonymous.register()
	}
	status, body := anonymous.do(http.MethodPost, "/v1/installs", nil)
	var refusal analytics.Refusal
	decode(t, body, &refusal)
	if status != http.StatusTooManyRequests || refusal.Limit != "installs_per_ip_per_hour" || refusal.Value != 20 || refusal.Observed != 21 || refusal.RetryAfterS < 1 {
		t.Fatalf("the 21st registration: %d %s", status, body)
	}
}

func TestOversizedRequestsAreRefusedNamingTheLimit(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()

	noise := make([]byte, 4<<20+1)
	_, _ = rand.Read(noise)
	status, body := install.do(http.MethodPost, "/v1/events", noise)
	var refusal analytics.Refusal
	decode(t, body, &refusal)
	if status != http.StatusRequestEntityTooLarge || refusal.Limit != "request_body_gzip_bytes" || refusal.Value != 4<<20 || refusal.Observed != 4<<20+1 {
		t.Fatalf("a body past 4 MB: %d %s", status, body)
	}

	status, body = install.send(analytics.Batch{Build: analytics.CurrentBuild(), Records: busRecords(1, 10_001)})
	decode(t, body, &refusal)
	if status != http.StatusRequestEntityTooLarge || refusal.Limit != "records_per_batch" || refusal.Value != 10_000 || refusal.Observed != 10_001 {
		t.Fatalf("a batch of 10001 records: %d %s", status, body)
	}

	build := analytics.CurrentBuild()
	build.Version = strings.Repeat("v", 2<<10)
	status, body = install.send(analytics.Batch{Build: build, Records: busRecords(1, 1)})
	decode(t, body, &refusal)
	if status != http.StatusRequestEntityTooLarge || refusal.Limit != "build_line_bytes" || refusal.Value != 2<<10 || refusal.Observed <= 2<<10 {
		t.Fatalf("a build line past 2 KiB: %d %s", status, body)
	}
	if events := install.storedEvents(clickHouse); events != "0" {
		t.Fatalf("refused requests stored %s events", events)
	}
}

func TestAQuarantinedInstallIsRefusedWithTheReason(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()
	clickHouse.Query(t, fmt.Sprintf("INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('%s', '', 'flooded garbage names', now64())", install.installID))

	status, body := install.send(analytics.Batch{Build: analytics.CurrentBuild(), Records: busRecords(1, 1)})
	if status != http.StatusForbidden || !bytes.Contains(body, []byte("flooded garbage names")) {
		t.Fatalf("a quarantined install's batch: %d %s", status, body)
	}

	ipHash := clickHouse.Query(t, fmt.Sprintf("SELECT ip_hash FROM installs WHERE install_id = '%s'", install.installID))
	clickHouse.Query(t, fmt.Sprintf("INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('', '%s', 'minted installs in a loop', now64())", ipHash))
	status, body = client{t: t, url: ingest.URL}.do(http.MethodPost, "/v1/installs", nil)
	if status != http.StatusForbidden || !bytes.Contains(body, []byte("minted installs in a loop")) {
		t.Fatalf("registration from a quarantined address: %d %s", status, body)
	}
}

func TestTheSameBatchSentConcurrentlyIsStoredOnce(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()
	batch := analytics.Batch{Build: analytics.CurrentBuild(), Records: busRecords(1, 2_000)}

	replies := make(chan analytics.Accepted, 4)
	for range 4 {
		go func() {
			var body bytes.Buffer
			_ = analytics.WriteBatch(&body, batch)
			req, _ := http.NewRequest(http.MethodPost, ingest.URL+"/v1/events", &body)
			req.Header.Set("Authorization", "Bearer "+install.token)
			var accepted analytics.Accepted
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = json.NewDecoder(resp.Body).Decode(&accepted)
				resp.Body.Close()
			}
			replies <- accepted
		}()
	}
	var total analytics.Accepted
	for range 4 {
		reply := <-replies
		total.Accepted += reply.Accepted
		total.Duplicates += reply.Duplicates
	}
	if total != (analytics.Accepted{Accepted: 2_000, Duplicates: 6_000}) {
		t.Fatalf("four concurrent copies of a 2000-record batch answered %+v in total", total)
	}
	if events := install.storedEvents(clickHouse); events != "2000" {
		t.Fatalf("ClickHouse holds %s events, want 2000", events)
	}
}

func TestABatchSpanningThousandsOfStreamsStoresOnlyTheNewOnes(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()
	streams := func(first, count int) []analytics.Record {
		records := make([]analytics.Record, count)
		for i := range records {
			records[i] = analytics.Record{
				Stream: analytics.Stream(fmt.Sprintf("s%031d", first+i)),
				Seq:    1 << 62,
				Name:   "made.up.stream",
				At:     time.Date(2026, 10, 19, 14, 2, 11, 0, time.UTC),
			}
		}
		return records
	}
	build := analytics.CurrentBuild()

	status, body := install.send(analytics.Batch{Build: build, Records: streams(0, 3_000)})
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"accepted":3000,"duplicates":0}` {
		t.Fatalf("3000 new streams: %d %s", status, body)
	}
	status, body = install.send(analytics.Batch{Build: build, Records: streams(0, 5_000)})
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"accepted":2000,"duplicates":3000}` {
		t.Fatalf("the 3000 stored streams resent with 2000 new ones: %d %s", status, body)
	}
	if stored := clickHouse.Query(t, "SELECT count(), uniqExact(stream) FROM events"); stored != "5000\t5000" {
		t.Fatalf("ClickHouse holds %q events and streams, want 5000 in 5000", stored)
	}
}

func TestEachRecordKindReadsBackAsTypedColumnsThroughItsView(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	service := clickHouse.User(t, "SELECT, INSERT, CREATE TABLE, ALTER DELETE, ALTER UPDATE, CREATE VIEW, DROP VIEW ON *")
	testworld.StartIngest(t, service)
	ingest := testworld.StartIngest(t, service)
	views := []string{"runs", "quiet_stops", "cli_commands", "process_samples", "daemon_starts", "app_actions"}
	grants := make([]string, len(views))
	for i, view := range views {
		grants[i] = "SELECT ON " + view
	}
	reader := clickHouse.User(t, grants...)
	if !reader.Refuses(t, "SELECT count() FROM events") {
		t.Fatal("a user granted only the views reads events")
	}
	install := client{t: t, url: ingest.URL}.register()
	build := analytics.CurrentBuild()

	at := func(clock string) time.Time {
		parsed, err := time.Parse(time.DateTime, clock)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	record := func(stream analytics.Stream, seq int64, name, subject, clock, props string) analytics.Record {
		return analytics.Record{Stream: stream, Seq: seq, Name: name, Subject: subject, At: at(clock), Props: json.RawMessage(props)}
	}
	outpostSample := analytics.StreamSample.OnMachine("h:0123456789ab")
	records := []analytics.Record{
		record(analytics.StreamRun, 1, "run", "h:aaaaaaaaaaaa", "2026-11-10 10:00:00",
			`{"started_by":"ring","duration_ms":61000,"ended":"waiting_input","opened_turn":false,"quiet_stop":true,"turn_requested":false,"turn_after_stop":"none","approvals":0,"usage":"known","tokens":{"input":10,"output":200,"cache_read":3000,"cache_write":40},"usd":0.25,"session":{"harness":"claude","model":"claude-opus-5-5","model_requested":"opus","launch":"delegation"}}`),
		record(analytics.StreamRun, 2, "quiet_stop", "h:aaaaaaaaaaaa", "2026-11-10 10:00:00", `{"verdict":"read"}`),
		record(analytics.StreamRun, 3, "run", "h:bbbbbbbbbbbb", "2026-11-10 11:00:00",
			`{"started_by":"user","duration_ms":5000,"ended":"waiting_input","opened_turn":true,"quiet_stop":false,"turn_requested":false,"turn_after_stop":"owed","user_input_ms":1200,"approvals":2,"usage":"unavailable","session":{"harness":"codex","launch":"user"}}`),
		record(analytics.StreamCLI, 1, "seed show", "", "2026-11-01 23:59:59", `{"ok":true,"exit_code":0,"caller":"human"}`),
		record(analytics.StreamCLI, 2, "seed harvest", "h:aaaaaaaaaaaa", "2026-11-10 10:01:00",
			`{"ok":false,"exit_code":1,"error_kind":"refused","refusal":"not_tender","caller":"agent","harness":"claude"}`),
		record(analytics.StreamCLI, 3, "_hook-state", "h:aaaaaaaaaaaa", "2026-11-10 10:02:00", `{"ok":true,"exit_code":0,"caller":"agent","harness":"claude"}`),
		record(analytics.StreamSample, 1, "daemon_start", "h:cccccccccccc", "2026-11-10 08:00:00", `{"previous_end":"stopped","stop_reason":"source_fingerprint_mismatch","previous_run":"h:dddddddddddd"}`),
		record(analytics.StreamSample, 2, "process", "h:cccccccccccc", "2026-11-10 09:00:00",
			`{"goroutines":80,"workload":{"sessions":9,"agents":7,"terminals":9,"panes_drawn":4,"desktops":3,"seeds":812},"processes":[{"role":"daemon","count":1,"resident_bytes":90000000,"footprint_bytes":70000000},{"role":"webkit_content","count":2,"resident_bytes":614000000,"footprint_bytes":1137000000,"split":{"webkit_malloc_bytes":602000000,"owned_graphics_bytes":398000000,"gigacage_bytes":41000000}},{"role":"app","shell":"native","count":1,"resident_bytes":50000000,"footprint_bytes":40000000}]}`),
		record(outpostSample, 1, "process", "h:eeeeeeeeeeee", "2026-11-10 09:30:00",
			`{"workload":{"sessions":1,"agents":1,"terminals":1,"panes_drawn":0,"desktops":0,"seeds":0},"processes":[{"role":"pty_host","count":1,"resident_bytes":20000000}]}`),
		record(analytics.StreamApp, 1, "shortcut", "", "2026-11-10 12:00:00", `{"id":"queue.next"}`),
		record(analytics.StreamApp, 2, "queue_advance", "", "2026-11-10 12:01:00", `{"reason":"prompt_sent","to":"session","wait_ms":90000}`),
		record(analytics.StreamApp, 3, "settings_section", "", "2026-11-10 12:02:00", `{"section":"analytics"}`),
	}
	if status, body := install.send(analytics.Batch{Build: build, Records: records}); status != http.StatusOK {
		t.Fatalf("send: %d %s", status, body)
	}

	for _, view := range []struct{ query, want string }{
		{
			"SELECT session, started_by, duration_ms, ended, opened_turn, quiet_stop, turn_requested, turn_after_stop, user_input_ms, approvals, usage, tokens_input, tokens_output, tokens_cache_read, tokens_cache_write, usd, harness, model, model_requested, launch, build_commit FROM runs ORDER BY at",
			"h:aaaaaaaaaaaa\tring\t61000\twaiting_input\tfalse\ttrue\tfalse\tnone\t\\N\t0\tknown\t10\t200\t3000\t40\t0.25\tclaude\tclaude-opus-5-5\topus\tdelegation\t" + build.Commit + "\n" +
				"h:bbbbbbbbbbbb\tuser\t5000\twaiting_input\ttrue\tfalse\tfalse\towed\t1200\t2\tunavailable\t\\N\t\\N\t\\N\t\\N\t\\N\tcodex\t\t\tuser\t" + build.Commit,
		},
		{
			"SELECT session, verdict, failure, turn_after_stop, turn_requested, started_by, harness, launch FROM quiet_stops",
			"h:aaaaaaaaaaaa\tread\t\tnone\tfalse\tring\tclaude\tdelegation",
		},
		{
			"SELECT session, command, error_kind, refusal, caller, harness, ok, exit_code FROM cli_commands ORDER BY at",
			"h:aaaaaaaaaaaa\tseed harvest\trefused\tnot_tender\tagent\tclaude\tfalse\t1\n" +
				"h:aaaaaaaaaaaa\t_hook-state\t\t\tagent\tclaude\ttrue\t0",
		},
		{
			"SELECT daemon_run, outpost, daemon_uptime_s, role, shell, count, resident_bytes, footprint_bytes, webkit_malloc_bytes, owned_graphics_bytes, gigacage_bytes, sessions, agents, terminals, panes_drawn, desktops, seeds FROM process_samples ORDER BY at, role",
			"h:cccccccccccc\t\t3600\tapp\tnative\t1\t50000000\t40000000\t\\N\t\\N\t\\N\t9\t7\t9\t4\t3\t812\n" +
				"h:cccccccccccc\t\t3600\tdaemon\t\t1\t90000000\t70000000\t\\N\t\\N\t\\N\t9\t7\t9\t4\t3\t812\n" +
				"h:cccccccccccc\t\t3600\twebkit_content\t\t2\t614000000\t1137000000\t602000000\t398000000\t41000000\t9\t7\t9\t4\t3\t812\n" +
				"h:eeeeeeeeeeee\th:0123456789ab\t\\N\tpty_host\t\t1\t20000000\t\\N\t\\N\t\\N\t\\N\t1\t1\t1\t0\t0\t0",
		},
		{
			"SELECT daemon_run, previous_end, stop_reason, previous_run, build_os FROM daemon_starts",
			"h:cccccccccccc\tstopped\tsource_fingerprint_mismatch\th:dddddddddddd\t" + build.Os,
		},
		{
			"SELECT action, id, section, reason, to, wait_ms FROM app_actions ORDER BY at",
			"shortcut\tqueue.next\t\t\t\t\\N\n" +
				"queue_advance\t\t\tprompt_sent\tsession\t90000\n" +
				"settings_section\t\tanalytics\t\t\t\\N",
		},
	} {
		if got := reader.Query(t, view.query); got != view.want {
			t.Errorf("%s\ngot:\n%s\nwant:\n%s", view.query, got, view.want)
		}
	}
}
