package pty

import "time"

type Source string

const (
	SourceWorkerInfo Source = "worker_info"
	SourceHeartbeat  Source = "heartbeat"
	SourceUnknown    Source = "unknown"
)

func (s Source) ClaimsProtocolState() bool {
	return s != SourceHeartbeat
}

type Observation struct {
	Source Source
	Claim  string
	Detail string
	At     time.Time
}

func newObservation(source Source, claim, detail string, at time.Time) Observation {
	return Observation{Source: source, Claim: claim, Detail: detail, At: at}
}
