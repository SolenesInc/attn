package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type resolvedFsRoot string

type notebookRequestScope struct {
	profileID string
	root      resolvedFsRoot
}

func (d *Daemon) resolveNotebookRequest(client *wsClient, cmd string, msg any) (notebookRequestScope, error) {
	scope := notebookRequestScope{profileID: client.selectedProfile()}
	if !strings.HasPrefix(cmd, "fs_") && !strings.HasPrefix(cmd, "notebook_") {
		return scope, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return scope, err
	}
	var request struct {
		ProfileID    string `json:"profile_id"`
		ExpectedRoot string `json:"expected_notebook_root"`
		Root         string `json:"root"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return scope, err
	}
	if cmd != protocol.CmdFsUnwatch && request.ProfileID != "" && request.ProfileID != scope.profileID {
		return scope, fmt.Errorf("notebook request belongs to profile %q; selected profile is %q", request.ProfileID, scope.profileID)
	}
	if cmd != protocol.CmdFsUnwatch && request.ExpectedRoot != "" {
		current, err := d.notebookRoot(scope.profileID)
		if err != nil {
			return scope, err
		}
		if current != request.ExpectedRoot {
			return scope, fmt.Errorf("notebook folder changed from %q to %q; reload before saving", request.ExpectedRoot, current)
		}
	}
	root, err := d.resolveFsRoot(client, request.Root)
	scope.root = resolvedFsRoot(root)
	return scope, err
}

func (d *Daemon) sendNotebookScopeError(client *wsClient, cmd string, msg any, err error) {
	raw, _ := json.Marshal(msg)
	var request struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &request)
	d.sendToClient(client, map[string]any{"event": cmd + "_result", "request_id": request.RequestID, "success": false, "error": err.Error()})
}
