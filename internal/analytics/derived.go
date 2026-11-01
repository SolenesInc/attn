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
	ClaudeMD bool `json:"claude_md"`
	Worktree bool `json:"worktree"`
	Remote   bool `json:"remote"`
}

var DerivedFields = Fields{
	"session.harness": Safe,
	"session.model":   Safe,
	"session.launch":  Safe,
	"session.state":   Safe,
}

func Props(payload json.RawMessage, fields Fields, secret []byte, derived Derived) json.RawMessage {
	props := Redact(payload, fields, secret)
	if derived == (Derived{}) {
		return props
	}
	encoded, err := json.Marshal(derived)
	if err != nil {
		return props
	}
	var traits map[string]json.RawMessage
	if err := json.Unmarshal(Redact(encoded, DerivedFields, nil), &traits); err != nil {
		return props
	}
	if len(props) > 0 {
		var payloadFields map[string]json.RawMessage
		if err := json.Unmarshal(props, &payloadFields); err != nil {
			return props
		}
		maps.Copy(payloadFields, traits)
		traits = payloadFields
	}
	merged, err := json.Marshal(traits)
	if err != nil {
		return props
	}
	return merged
}
