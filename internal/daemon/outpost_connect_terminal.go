package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleConnectOutpostInTerminal(client *wsClient, msg *protocol.ConnectOutpostInTerminalMessage) {
	d.life.Go("connectOutpostInTerminal", func() {
		if err := d.connectOutpostInTerminal(client, msg.ID); err != nil {
			d.sendCommandError(client, protocol.CmdConnectOutpostInTerminal, err.Error())
		}
	})
}

func (d *Daemon) connectOutpostInTerminal(client *wsClient, id string) error {
	r := &d.outposts
	r.actions.Lock()
	defer r.actions.Unlock()
	if !d.outpostsEnabled() {
		return fmt.Errorf("outposts_enabled is off; turn Outposts on before connecting in a terminal")
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return err
	}
	var row store.Outpost
	for _, saved := range rows {
		if saved.ID == id {
			row = saved
			break
		}
	}
	if row.ID == "" {
		return fmt.Errorf("outpost %q does not exist", id)
	}
	r.mu.Lock()
	run := r.dialers[id]
	var commandSession protocol.SessionID
	connectable := run != nil && run.connectable
	if run != nil {
		commandSession = run.commandSession
	}
	r.mu.Unlock()
	if commandSession != "" {
		session := d.store.Get(commandSession)
		if session == nil || session.ProfileID != client.selectedProfile() {
			return fmt.Errorf("this outpost already has a connection terminal open in another profile")
		}
		desktop, placed, err := d.store.SessionPlacement(commandSession)
		if err != nil {
			return err
		}
		if !placed {
			return fmt.Errorf("connection terminal has no tile")
		}
		if _, err = d.store.SetCurrentDesktop(session.ProfileID, desktop.DesktopID); err != nil {
			return err
		}
		if _, _, err = d.store.SetActivePane(desktop.DesktopID, desktop.PaneID); err != nil {
			return err
		}
		d.publishArrangementChanged(session.ProfileID)
		return nil
	}
	if !connectable {
		return fmt.Errorf("outpost %s has no ssh refusal to answer in a terminal", row.Name)
	}
	argv := d.outpostSSH(row).MasterCommand()
	argv[0], err = exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	d.stopOutpostDial(id)
	session := protocol.SessionID(uuid.NewString())
	_, rejection := d.runSpawnPipelineReporting(&protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: session, ProfileID: client.selectedProfile(),
		Agent: protocol.AgentShellValue, Label: protocol.Ptr("Connect to " + row.Name), Cwd: "~", Cols: 80, Rows: 24,
	}, internalSpawnPolicy{userStarted: true, requester: client,
		launchPlacement: &launchPlacement{direction: layouttree.DirectionVertical},
		command:         &store.TerminalCommand{Argv: argv, OutpostID: id},
	})
	d.startOutpostDial(row)
	if rejection != nil {
		return rejection.reason()
	}
	d.publishFact(FactOutpostChanged, id, nil)
	return nil
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
	commands, err := d.store.UnfinishedCommandTerminals(id)
	if err != nil {
		d.logf("read outpost connection terminals: %v", err)
		return
	}
	for _, command := range commands {
		closing, err := d.beginSessionClose(command.SessionID, store.SessionClose{By: store.SessionClosedByUser, Reason: "outpost connection cancelled"}, nil)
		if err != nil {
			d.logf("close outpost connection terminal: %v", err)
			continue
		}
		if closing.teardown != nil {
			<-d.terminateSessionAsync(command.SessionID, syscall.SIGTERM, closing.teardown)
		}
	}
}

func (d *Daemon) reconcileOutpostCommandTerminals() {
	live := d.liveSessions(context.Background())
	commands, err := d.store.UnfinishedCommandTerminals("")
	if err != nil {
		d.logf("read outpost connection terminals after recovery: %v", err)
		return
	}
	for _, command := range commands {
		if _, running := live[command.SessionID]; !running {
			d.releaseOutpostCommand(command.SessionID, command.OutpostID, -1)
		}
	}
}
