package protocol

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON rejects retired delegation spellings instead of silently ignoring them.
// The daemon recovery path detects persisted old-shape operations before decoding.
func (m *DelegateMessage) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"brief", "ticket_id", "confirm", "placement", "workspace_id", "worktree", "plot", "handover", "preferences_revision"} {
		if _, found := fields[name]; found {
			return fmt.Errorf("delegate field %q is retired; use assignment, cwd, and checkout", name)
		}
	}
	type plain DelegateMessage
	return json.Unmarshal(data, (*plain)(m))
}
