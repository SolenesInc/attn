package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) agentCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"agent list":       command.Typed(d.runAgentList),
		"agent peek":       command.Typed(d.runAgentPeek),
		"agent msg":        command.Typed(d.runAgentMsg),
		"agent close":      command.Typed(d.runAgentClose),
		"agent reload":     command.Typed(d.runAgentReload),
		"agent inbox":      command.Typed(d.runAgentInbox),
		"agent msg-status": command.Typed(d.runAgentMsgStatus),
		"agent attachment": command.Typed(d.runAgentAttachment),
	}
}

func (d *Daemon) runAgentList(ctx context.Context, c command.Caller, a command.AgentListArgs) (any, error) {
	profiles, err := d.liveProtocolProfiles()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	rows := []protocol.AgentListRow{}
	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		if !c.Sees(session.ProfileID) {
			continue
		}
		row := protocol.AgentListRow{
			ID: string(session.ID), Label: session.Label, Agent: string(session.Agent), Profile: names[session.ProfileID],
			Directory: session.Directory, State: string(session.State), TurnOwed: protocol.Deref(session.TurnOwed),
			Member: protocol.Deref(session.CrewMemberName),
		}
		if key := protocol.Deref(session.CrewMember); key != "" {
			row.MemberKey = protocol.Ptr(key)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Profile != rows[j].Profile {
			return rows[i].Profile < rows[j].Profile
		}
		return rows[i].Label < rows[j].Label
	})
	return protocol.AgentListResult{Agents: rows}, nil
}

func (d *Daemon) runAgentAttachment(ctx context.Context, c command.Caller, a command.AgentAttachmentArgs) (any, error) {
	path := a.Out
	if !filepath.IsAbs(path) {
		return nil, command.Usage("{out} must be an absolute path, not %q", path)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".attn-attachment-*")
	if err != nil {
		return nil, err
	}
	defer func() { temp.Close(); os.Remove(temp.Name()) }()
	msg := protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: a.Capture, AttachmentID: a.Attachment}
	if c.Session != "" {
		msg.SourceSessionID = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		msg.ProfileID = protocol.Ptr(c.Profile)
	}
	for {
		result, err := Call[protocol.QuickCaptureResult](ctx, d.router(), msg)
		if err != nil {
			return nil, err
		}
		chunk := result.Download
		if chunk == nil {
			return nil, errors.New("the home returned no file chunk")
		}
		data, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			return nil, err
		}
		if chunk.NextOffset != msg.Offset+len(data) || (!chunk.Eof && len(data) == 0) {
			return nil, errors.New("file download offset receipt disagrees with bytes")
		}
		if _, err := temp.Write(data); err != nil {
			return nil, err
		}
		msg.Offset = chunk.NextOffset
		if chunk.Eof {
			break
		}
	}
	if err := temp.Sync(); err != nil {
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(temp.Name(), path); err != nil {
		return nil, err
	}
	return prompts.RenderText("session", "quick-capture-file-saved", prompts.Values{"path": path}), nil
}
