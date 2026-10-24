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

type Build struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Channel     string `json:"channel"`
	BuildTime   string `json:"build_time"`
	Fingerprint string `json:"fingerprint"`
	Protocol    string `json:"protocol"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Dirty       bool   `json:"dirty"`
	AppVersion  string `json:"app_version,omitempty"`
	AppChannel  string `json:"app_channel,omitempty"`
}

type Batch struct {
	Build   Build    `json:"build"`
	Records []Record `json:"records"`
}
