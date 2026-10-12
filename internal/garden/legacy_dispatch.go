package garden

import (
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
)

type Dispatch struct {
	SessionID         protocol.SessionID `json:"session_id"`
	Crown             string             `json:"crown"`
	DispatcherSession protocol.SessionID `json:"dispatcher_session,omitempty"`
	DispatcherMember  string             `json:"dispatcher_member,omitempty"`
	Cwd               string             `json:"cwd,omitempty"`
	Agent             string             `json:"agent,omitempty"`
	HostKind          string             `json:"host_kind,omitempty"`
	EndpointID        string             `json:"endpoint_id,omitempty"`
	RepositoryRoot    string             `json:"repository_root,omitempty"`
	RepositorySubdir  string             `json:"repository_subdir,omitempty"`
	Branch            string             `json:"branch,omitempty"`
	CapturedAt        string             `json:"captured_at,omitempty"`
	SupersededBy      protocol.SessionID `json:"superseded_by,omitempty"`
	OperationID       string             `json:"operation_id,omitempty"`
	FromChief         bool               `json:"from_chief,omitempty"`
	Resume            string             `json:"resume,omitempty"`
}

func (d Dispatch) Encode() ([]byte, error) { return json.Marshal(d) }

func DecodeDispatch(body []byte) (Dispatch, error) {
	var dispatch Dispatch
	if err := json.Unmarshal(body, &dispatch); err != nil {
		return Dispatch{}, fmt.Errorf("this dispatch's stored body is not readable: %w", err)
	}
	return dispatch, nil
}
