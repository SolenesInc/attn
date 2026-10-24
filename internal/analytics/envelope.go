package analytics

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"time"
)

type Stream string

const (
	StreamBus    Stream = "bus"
	StreamApp    Stream = "app"
	StreamCLI    Stream = "cli"
	StreamSample Stream = "sample"
	StreamVent   Stream = "vent"
)

type Record struct {
	Stream  Stream          `json:"stream"`
	Seq     int64           `json:"seq"`
	Name    string          `json:"name"`
	Subject string          `json:"subject,omitempty"`
	At      time.Time       `json:"at"`
	Bytes   int             `json:"bytes,omitempty"`
	Props   json.RawMessage `json:"props,omitempty"`
}

type Batch struct {
	Build   Build
	Records []Record
}

func WriteBatch(w io.Writer, batch Batch) error {
	zw := gzip.NewWriter(w)
	enc := json.NewEncoder(zw)
	if err := enc.Encode(struct {
		Build Build `json:"build"`
	}{batch.Build}); err != nil {
		return err
	}
	for _, record := range batch.Records {
		if err := enc.Encode(record); err != nil {
			return err
		}
	}
	return zw.Close()
}
