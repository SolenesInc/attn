package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) closeAgentMachine(_ context.Context, msg protocol.AgentLaunchCloseMessage) (protocol.AgentLaunchCloseResult, error) {
	monitor := d.sessionMonitor(msg.SessionID)
	result := protocol.AgentLaunchCloseResult{}
	switch msg.Phase {
	case "prepare":
		monitor.MarkForcedStop()
		driver := monitor.EndDriverRun()
		raw, err := json.Marshal(driver)
		if err != nil {
			return result, err
		}
		result.Driver = protocol.Ptr(string(raw))
	case "cancel":
		monitor.ClearForcedStop()
		monitor.ResumeUsage()
		monitor.ResetInput()
		monitor.EnsureTranscriptWatcher(d.store.GetSessionConversation(msg.SessionID).TranscriptPath)
		var driver sessionmonitor.DriverMetadata
		if msg.Driver != nil {
			if err := json.Unmarshal([]byte(*msg.Driver), &driver); err != nil {
				return result, err
			}
			if driver.Run != "" {
				monitor.RestoreDriverRun(driver)
			}
		}
	case "drain":
		if monitor == nil {
			return result, nil
		}
		monitor.FenceInput()
		session := d.store.Get(msg.SessionID)
		if session != nil {
			monitor.FinishAndSettleUsage(session.Agent, d.store.GetSessionConversation(session.ID).TranscriptPath)
		} else {
			monitor.FinishUsage()
		}
		view := monitor.Snapshot()
		if !monitor.Publish(view) {
			return result, fmt.Errorf("final monitor view for %s was not saved", msg.SessionID)
		}
		raw, err := json.Marshal(view)
		if err != nil {
			return result, err
		}
		result.View = protocol.Ptr(string(raw))
		if d.store.SessionLink(msg.SessionID) == codexLinkKind && session != nil {
			r := d.codexShared()
			r.mu.Lock()
			r.archivingAfterClose[session.ProfileID]++
			r.mu.Unlock()
		}
	case "committed":
		if msg.Profile == nil {
			d.forgetMonitor(msg.SessionID)
		} else {
			r := d.codexShared()
			profile := *msg.Profile
			defer func() {
				r.mu.Lock()
				if r.archivingAfterClose[profile]--; r.archivingAfterClose[profile] == 0 {
					delete(r.archivingAfterClose, profile)
				}
				r.mu.Unlock()
				r.profileReleased(profile)
			}()
			if protocol.Deref(msg.Archive) {
				r.archiveConversation(profile, msg.SessionID, protocol.Deref(msg.Conversation))
			}
		}
	case "notify":
		var driver sessionmonitor.DriverMetadata
		if msg.Driver != nil {
			if err := json.Unmarshal([]byte(*msg.Driver), &driver); err != nil {
				return result, err
			}
		}
		if driver.Run != "" {
			d.notifyPluginDriverSessionClosed(driver.Plugin, protocol.Deref(msg.TerminalID), driver.Run, "killed", nil, protocol.Deref(msg.Signal))
		}
	default:
		return result, fmt.Errorf("unknown agent close phase %q", msg.Phase)
	}
	return result, nil
}

func (d *Daemon) agentCloseCall(msg protocol.AgentLaunchCloseMessage) (protocol.AgentLaunchCloseResult, error) {
	msg.Cmd = protocol.CmdAgentLaunchClose
	return Call[protocol.AgentLaunchCloseResult](context.Background(), d.router(), msg)
}

func (d *Daemon) drainAgentBeforeClose(sessionID protocol.SessionID) error {
	result, err := d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "drain"})
	if err != nil {
		return err
	}
	if result.View == nil || d.sessionHost(sessionID) == "" {
		return nil
	}
	var view sessionmonitor.View
	if err := json.Unmarshal([]byte(*result.View), &view); err != nil {
		return err
	}
	if !d.applyHomeView(sessionID, view, "", nil) {
		return fmt.Errorf("final monitor view for %s was not applied", sessionID)
	}
	return nil
}
