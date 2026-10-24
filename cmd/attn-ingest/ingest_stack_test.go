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
	t     *testing.T
	url   string
	token string
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

func (c client) holdings() analytics.InstallHoldings {
	c.t.Helper()
	status, body := c.do(http.MethodGet, "/v1/installs/me", nil)
	if status != http.StatusOK {
		c.t.Fatalf("GET /v1/installs/me: %d %s", status, body)
	}
	var holdings analytics.InstallHoldings
	decode(c.t, body, &holdings)
	return holdings
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
	holdings := install.holdings()
	if holdings.Events != 100 || holdings.Streams[analytics.StreamBus] != 99 || holdings.Streams[analytics.StreamApp] != 1 {
		t.Fatalf("after a retry the server holds %+v, want 99 bus and 1 app event", holdings)
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
	if holdings := install.holdings(); holdings.Events != 100 {
		t.Fatalf("a refused batch stored rows: the server holds %d events", holdings.Events)
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
	if status, _ := install.do(http.MethodGet, "/v1/installs/me", nil); status != http.StatusUnauthorized {
		t.Fatalf("the deleted install's token still answers GET /v1/installs/me with %d", status)
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
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t))
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
	if holdings := fresh.holdings(); holdings.Events != 270 {
		t.Fatalf("a fresh install's first batch stored %d events, want 270", holdings.Events)
	}
}

func TestAllInstallsTogetherStoreAtMostTheConfiguredBytesADay(t *testing.T) {
	const budget = int64(64 << 10)
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t), fmt.Sprintf("ATTN_INGEST_BYTES_PER_DAY=%d", budget))
	anonymous := client{t: t, url: ingest.URL}
	body, plainBytes := encodedBatch(t, busRecords(1, 100))
	sender := &bytesSender{t: t, body: body, plainBytes: plainBytes}

	for sender.sent < budget/plainBytes {
		sender.accept(anonymous.register())
	}
	late := anonymous.register()
	sender.refused(late, "bytes_per_day", budget, (budget/plainBytes+1)*plainBytes)
	if holdings := late.holdings(); holdings.Events != 0 {
		t.Fatalf("a batch past the budget for all installs stored %d events", holdings.Events)
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
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t))
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
	if holdings := install.holdings(); holdings.Events != 0 {
		t.Fatalf("refused requests stored %d events", holdings.Events)
	}
}

func TestAQuarantinedInstallIsRefusedWithTheReason(t *testing.T) {
	clickHouse := testworld.NewClickHouse(t)
	ingest := testworld.StartIngest(t, clickHouse)
	install := client{t: t, url: ingest.URL}.register()
	installID := install.holdings().InstallID
	clickHouse.Query(t, fmt.Sprintf("INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('%s', '', 'flooded garbage names', now64())", installID))

	status, body := install.send(analytics.Batch{Build: analytics.CurrentBuild(), Records: busRecords(1, 1)})
	if status != http.StatusForbidden || !bytes.Contains(body, []byte("flooded garbage names")) {
		t.Fatalf("a quarantined install's batch: %d %s", status, body)
	}

	ipHash := clickHouse.Query(t, fmt.Sprintf("SELECT ip_hash FROM installs WHERE install_id = '%s'", installID))
	clickHouse.Query(t, fmt.Sprintf("INSERT INTO quarantine (install_id, ip_hash, reason, at) VALUES ('', '%s', 'minted installs in a loop', now64())", ipHash))
	status, body = client{t: t, url: ingest.URL}.do(http.MethodPost, "/v1/installs", nil)
	if status != http.StatusForbidden || !bytes.Contains(body, []byte("minted installs in a loop")) {
		t.Fatalf("registration from a quarantined address: %d %s", status, body)
	}
}

func TestTheSameBatchSentConcurrentlyIsStoredOnce(t *testing.T) {
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t))
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
	if holdings := install.holdings(); holdings.Events != 2_000 {
		t.Fatalf("the server holds %d events, want 2000", holdings.Events)
	}
}

func TestABatchSpanningThousandsOfStreamsStoresOnlyTheNewOnes(t *testing.T) {
	ingest := testworld.StartIngest(t, testworld.NewClickHouse(t))
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
	if holdings := install.holdings(); holdings.Events != 5_000 || len(holdings.Streams) != 5_000 {
		t.Fatalf("the server holds %d events in %d streams, want 5000 in 5000", holdings.Events, len(holdings.Streams))
	}
}
