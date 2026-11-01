package daemon_test

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEachUploadBatchCarriesTheBuildThatRecordedItsRecords(t *testing.T) {
	t.Parallel()
	ingest := startBatchRecorder(t)
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, config.AnalyticsIngestURLEnv+"="+ingest.URL)

	s.RunFrom(testworld.AttnBinaryAt(t, "0.1.0"))
	s.Start()
	app := s.App()
	setAnalyticsEnabled(app, "true")
	sendAppAction(app, "settings_section", map[string]any{"section": "under-0.1.0"})
	recordedByFirst := map[[2]any]bool{}
	for _, row := range listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000}).Records {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		recordedByFirst[[2]any{record.Stream, record.Seq}] = true
	}
	s.Stop()

	s.RunFrom(testworld.AttnBinaryAt(t, "0.2.0"))
	s.Start()
	app = s.App()
	sendAppAction(app, "settings_section", map[string]any{"section": "under-0.2.0"})
	setSetting(t, app, "analytics.sharing_enabled", "true")

	shippedIn := map[string]string{}
	for len(shippedIn) < 2 {
		var batch analytics.Batch
		select {
		case batch = <-ingest.batches:
		case <-time.After(fakeagent.HangGuard):
			t.Fatalf("attn-ingest received batches for %v within %s, want one from each build", shippedIn, fakeagent.HangGuard)
		}
		for _, record := range batch.Records {
			if recordedByFirst[[2]any{record.Stream, record.Seq}] && batch.Build.Version != "0.1.0" {
				t.Errorf("%s/%d %s was recorded by 0.1.0 but shipped in a %s batch", record.Stream, record.Seq, record.Name, batch.Build.Version)
			}
			var props struct {
				Section string `json:"section"`
			}
			_ = json.Unmarshal(record.Props, &props)
			if props.Section != "" {
				shippedIn[props.Section] = batch.Build.Version
				if batch.Build.AppVersion != testworld.AppVersion {
					t.Errorf("%s shipped with app version %q, want the app connected while it was recorded, %q", props.Section, batch.Build.AppVersion, testworld.AppVersion)
				}
			}
		}
		setSetting(t, app, "analytics.sharing_enabled", "true")
	}
	if shippedIn["under-0.1.0"] != "0.1.0" || shippedIn["under-0.2.0"] != "0.2.0" {
		t.Fatalf("records shipped in batches %v, want each under the build that recorded it", shippedIn)
	}
}

type batchRecorder struct {
	URL     string
	batches chan analytics.Batch
}

func startBatchRecorder(t *testing.T) batchRecorder {
	t.Helper()
	recorder := batchRecorder{batches: make(chan analytics.Batch, 16)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/installs":
			_ = json.NewEncoder(w).Encode(analytics.Registration{InstallID: "install", Token: "token"})
		case "/v1/events":
			batch, err := readBatch(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			recorder.batches <- batch
			_ = json.NewEncoder(w).Encode(analytics.Accepted{Accepted: len(batch.Records)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	recorder.URL = server.URL
	return recorder
}

func readBatch(r *http.Request) (analytics.Batch, error) {
	var batch analytics.Batch
	body, err := gzip.NewReader(r.Body)
	if err != nil {
		return batch, err
	}
	lines := bufio.NewScanner(body)
	lines.Buffer(nil, 1<<20)
	var header struct {
		Build analytics.Build `json:"build"`
	}
	if lines.Scan() {
		if err := json.Unmarshal(lines.Bytes(), &header); err != nil {
			return batch, err
		}
	}
	batch.Build = header.Build
	for lines.Scan() {
		var record analytics.Record
		if err := json.Unmarshal(lines.Bytes(), &record); err != nil {
			return batch, err
		}
		batch.Records = append(batch.Records, record)
	}
	return batch, lines.Err()
}
