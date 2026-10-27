package analytics

import "time"

type Registration struct {
	InstallID string `json:"install_id"`
	Token     string `json:"token"`
}

type Accepted struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
}

type Refusal struct {
	Limit       string `json:"limit"`
	Value       int64  `json:"value"`
	Observed    int64  `json:"observed"`
	RetryAfterS int64  `json:"retry_after_s,omitempty"`
}

type InstallHoldings struct {
	InstallID string           `json:"install_id"`
	CreatedAt time.Time        `json:"created_at"`
	Events    int64            `json:"events"`
	Streams   map[Stream]int64 `json:"streams"`
	FirstAt   *time.Time       `json:"first_at,omitempty"`
	LastAt    *time.Time       `json:"last_at,omitempty"`
}
