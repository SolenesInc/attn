package analytics

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
