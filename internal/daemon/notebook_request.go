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
	requestID string
}

func (d *Daemon) resolveNotebookRequest(client *wsClient, cmd string, raw []byte) (notebookRequestScope, error) {
	scope := notebookRequestScope{profileID: client.selectedProfile()}
	if !strings.HasPrefix(cmd, "fs_") && !strings.HasPrefix(cmd, "notebook_") {
		return scope, nil
	}
	var request struct {
		ProfileID    string `json:"profile_id"`
		ExpectedRoot string `json:"expected_notebook_root"`
		Root         string `json:"root"`
		RequestID    string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return scope, err
	}
	scope.requestID = request.RequestID
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

func (d *Daemon) sendNotebookScopeError(client *wsClient, cmd string, scope notebookRequestScope, err error) {
	message := err.Error()
	var reply any
	switch cmd {
	case protocol.CmdFsList:
		reply = protocol.FsListResultMessage{Event: protocol.EventFsListResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsRead:
		reply = protocol.FsReadResultMessage{Event: protocol.EventFsReadResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsReadAsset:
		reply = protocol.FsReadAssetResultMessage{Event: protocol.EventFsReadAssetResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsWrite:
		reply = protocol.FsWriteResultMessage{Event: protocol.EventFsWriteResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsRename:
		reply = protocol.FsRenameResultMessage{Event: protocol.EventFsRenameResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsDelete:
		reply = protocol.FsDeleteResultMessage{Event: protocol.EventFsDeleteResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsExists:
		reply = protocol.FsExistsResultMessage{Event: protocol.EventFsExistsResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsWatch:
		reply = protocol.FsWatchResultMessage{Event: protocol.EventFsWatchResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsUnwatch:
		reply = protocol.FsUnwatchResultMessage{Event: protocol.EventFsUnwatchResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdFsIndex:
		reply = protocol.FsIndexResultMessage{Event: protocol.EventFsIndexResult, RequestID: scope.requestID, Success: false, Error: &message, Files: []string{}}
	case protocol.CmdNotebookList:
		reply = protocol.NotebookListResultMessage{Event: protocol.EventNotebookListResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdNotebookRead:
		reply = protocol.NotebookReadResultMessage{Event: protocol.EventNotebookReadResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdNotebookBacklinks:
		reply = protocol.NotebookBacklinksResultMessage{Event: protocol.EventNotebookBacklinksResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdNotebookWrite:
		reply = protocol.NotebookWriteResultMessage{Event: protocol.EventNotebookWriteResult, RequestID: scope.requestID, Success: false, Error: &message}
	case protocol.CmdNotebookSendToChief:
		reply = protocol.NotebookSendToChiefResultMessage{Event: protocol.EventNotebookSendToChiefResult, RequestID: scope.requestID, Success: false, Error: &message}
	default:
		d.sendCommandError(client, cmd, message)
		return
	}
	d.sendToClient(client, reply)
}
