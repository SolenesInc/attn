package analytics

import (
	"encoding/json"
	"maps"
)

type Launch string

const (
	LaunchUser       Launch = "user"
	LaunchDelegation Launch = "delegation"
	LaunchAutomation Launch = "automation"
	LaunchCrew       Launch = "crew"
	LaunchChief      Launch = "chief"
)

type Derived struct {
	Session *SessionTraits `json:"session,omitempty"`
	Repo    *RepoTraits    `json:"repo,omitempty"`
}

type SessionTraits struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Launch  Launch `json:"launch"`
	State   string `json:"state"`
}

type RepoTraits struct {
	AgentsMD bool `json:"agents_md"`
	Worktree bool `json:"worktree"`
	Remote   bool `json:"remote"`
}

var DerivedSafeValues = Allowlist{
	"session.harness": true,
	"session.model":   true,
	"session.launch":  true,
	"session.state":   true,
}

func Props(payload json.RawMessage, allow Allowlist, derived Derived) json.RawMessage {
	props := Redact(payload, allow)
	if derived == (Derived{}) {
		return props
	}
	encoded, err := json.Marshal(derived)
	if err != nil {
		return props
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(Redact(encoded, DerivedSafeValues), &fields); err != nil {
		return props
	}
	if len(props) > 0 {
		var payloadFields map[string]json.RawMessage
		if err := json.Unmarshal(props, &payloadFields); err != nil {
			return props
		}
		maps.Copy(payloadFields, fields)
		fields = payloadFields
	}
	merged, err := json.Marshal(fields)
	if err != nil {
		return props
	}
	return merged
}
