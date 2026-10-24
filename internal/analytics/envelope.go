package analytics

import (
	"encoding/json"
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
