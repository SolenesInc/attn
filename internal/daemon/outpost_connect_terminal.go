package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleConnectOutpostInTerminal(client *wsClient, msg *protocol.ConnectOutpostInTerminalMessage) {
	d.life.Go("connectOutpostInTerminal", func() {
		d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
			placed, err := d.connectOutpostInTerminal(client, msg.ID)
			if err != nil {
				return profileActionOutcome{}, err
			}
			desktop, err := d.store.GetDesktop(placed.desktopID)
			return profileActionOutcome{desktops: []profiles.Desktop{desktop}, paneID: placed.paneID, arranges: true}, err
		})
	})
}

func (d *Daemon) connectOutpostInTerminal(client *wsClient, id string) (placementOutcome, error) {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	row, commandSession, connectable, err := d.outpostCommandTarget(id)
	if err != nil {
		return placementOutcome{}, err
	}
	if commandSession != "" {
		session := d.store.Get(commandSession)
		if session == nil || session.ProfileID != client.selectedProfile() {
			return placementOutcome{}, fmt.Errorf("this outpost already has a connection terminal open in another profile")
		}
		desktop, placed, err := d.store.SessionPlacement(commandSession)
		if err != nil {
			return placementOutcome{}, err
		}
		if !placed {
			return placementOutcome{}, fmt.Errorf("connection terminal has no tile")
		}
		if _, err = d.store.SetCurrentDesktop(session.ProfileID, desktop.DesktopID); err != nil {
			return placementOutcome{}, err
		}
		if _, _, err = d.store.SetActivePane(desktop.DesktopID, desktop.PaneID); err != nil {
			return placementOutcome{}, err
		}
		d.publishArrangementChanged(session.ProfileID)
		return placementOutcome{session: commandSession, desktopID: desktop.DesktopID, paneID: desktop.PaneID}, nil
	}
	return d.launchOutpostCommand(row, connectable, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), ProfileID: client.selectedProfile(),
		Agent: protocol.AgentShellValue, Label: protocol.Ptr("Connect to " + row.Name), Cwd: "~", Cols: 80, Rows: 24,
	}, internalSpawnPolicy{userStarted: true, launchPlacement: &launchPlacement{direction: layouttree.DirectionVertical}})
}

func (d *Daemon) retryOutpostCommand(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) error {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	row, commandSession, connectable, err := d.outpostCommandTarget(policy.command.OutpostID)
	if err != nil {
		return err
	}
	if commandSession == msg.ID {
		return fmt.Errorf("ssh in this terminal is still finishing; try again in a moment")
	}
	if commandSession != "" {
		return fmt.Errorf("outpost %s already has another connection terminal open; answer ssh there", row.Name)
	}
	_, err = d.launchOutpostCommand(row, connectable, msg, policy)
	return err
}

func (d *Daemon) outpostCommandTarget(id string) (store.Outpost, protocol.SessionID, bool, error) {
	if !d.outpostsEnabled() {
		return store.Outpost{}, "", false, fmt.Errorf("outposts_enabled is off; turn Outposts on before connecting in a terminal")
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return store.Outpost{}, "", false, err
	}
	var row store.Outpost
	for _, saved := range rows {
		if saved.ID == id {
			row = saved
			break
		}
	}
	if row.ID == "" {
		return store.Outpost{}, "", false, fmt.Errorf("outpost %q does not exist", id)
	}
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.dialers[id]
	if run == nil {
		return row, "", false, nil
	}
	return row, run.commandSession, run.connectable, nil
}

func (d *Daemon) launchOutpostCommand(row store.Outpost, connectable bool, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placementOutcome, error) {
	if !connectable {
		return placementOutcome{}, fmt.Errorf("outpost %s has no ssh refusal to answer in a terminal", row.Name)
	}
	argv := d.outpostSSH(row).MasterCommand()
	var err error
	argv[0], err = exec.LookPath(argv[0])
	if err != nil {
		return placementOutcome{}, err
	}
	policy.command = &store.TerminalCommand{Argv: argv, OutpostID: row.ID}
	d.stopOutpostDial(row.ID)
	placed, rejection := d.runSpawnPipelineReporting(msg, policy)
	d.startOutpostDial(row)
	if rejection != nil {
		return placementOutcome{}, rejection.reason()
	}
	d.publishFact(FactOutpostChanged, row.ID, nil)
	return placed, nil
}

func (d *Daemon) finishCommandTerminal(session protocol.SessionID, exit sessionmonitor.Exit) bool {
	intent, _ := d.store.LaunchIntent(session)
	if intent.Command == nil {
		return false
	}
	code := exit.Code
	if exit.Stopped || exit.Signal != "" {
		code = -1
	}
	d.releaseOutpostCommand(session, intent.Command.OutpostID, code)
	return true
}

func (d *Daemon) releaseOutpostCommand(session protocol.SessionID, id string, code int) {
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.dialers[id]
	if run == nil || run.commandSession != session {
		return
	}
	run.commandSession = ""
	run.commandExit <- code
}

func (d *Daemon) closeOutpostCommand(id string) {
	commands, err := d.store.UnfinishedOutpostTerminals(id)
	if err != nil {
		d.logf("read outpost connection terminals: %v", err)
		return
	}
	for _, session := range commands {
		closing, err := d.beginSessionClose(session, store.SessionClose{By: who.User(), Reason: "outpost connection cancelled"}, nil)
		if err != nil {
			d.logf("close outpost connection terminal: %v", err)
			continue
		}
		if closing.teardown != nil {
			<-d.terminateSessionAsync(session, syscall.SIGTERM, closing.teardown)
		}
	}
}

func (d *Daemon) reconcileOutpostCommandTerminals() {
	live := d.liveSessions(context.Background())
	waiting := map[protocol.SessionID]string{}
	d.outposts.mu.Lock()
	for id, run := range d.outposts.dialers {
		if run.commandSession != "" {
			waiting[run.commandSession] = id
		}
	}
	d.outposts.mu.Unlock()
	for session, outpost := range waiting {
		if exit := d.store.GetSessionExit(session); exit != nil {
			code := exit.Code
			if protocol.Deref(exit.Signal) != "" {
				code = -1
			}
			d.releaseOutpostCommand(session, outpost, code)
		} else if _, running := live[session]; !running {
			d.releaseOutpostCommand(session, outpost, -1)
		}
	}
}
