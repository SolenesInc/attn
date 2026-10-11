package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleGetFileDiffWS(client *wsClient, msg *protocol.GetFileDiffMessage) {
	d.logf("Getting file diff for %s in %s", msg.Path, msg.Directory)
	d.life.Go("handleGetFileDiff", func() { d.handleGetFileDiff(client, msg) })
}

func (d *Daemon) handleGetFileDiff(client *wsClient, msg *protocol.GetFileDiffMessage) {
	d.handleGetFileDiffForeground(context.Background(), client, msg)
}

func (d *Daemon) handleGetFileDiffForeground(ctx context.Context, client *wsClient, msg *protocol.GetFileDiffMessage) {
	result := protocol.FileDiffResultMessage{
		Event:     protocol.EventFileDiffResult,
		Directory: msg.Directory,
		Path:      msg.Path,
		Success:   false,
		RequestID: msg.RequestID,
	}

	baseRef := "HEAD"
	if msg.BaseRef != nil && *msg.BaseRef != "" {
		baseRef = *msg.BaseRef
	}

	headRef := ""
	if msg.HeadRef != nil {
		headRef = *msg.HeadRef
	}

	staged := msg.Staged != nil && *msg.Staged
	content, err := d.diffReader().FileDiff(ctx, msg.Directory, msg.Path, baseRef, headRef, staged)
	if err != nil {
		result.Error = protocol.Ptr("Failed to read file diff: " + err.Error())
		d.sendToClient(client, result)
		return
	}

	result.Original = content.original
	result.Modified = content.modified
	result.Success = true
	d.sendToClient(client, result)
}
