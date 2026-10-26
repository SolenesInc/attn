package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

func outposts(args []string) error {
	fs := flag.NewFlagSet("outposts", flag.ContinueOnError)
	add := fs.String("add", "", "add an outpost with this name")
	address := fs.String("address", "", "address:port for --add")
	remove := fs.String("remove", "", "remove this outpost id")
	repair := fs.String("repair", "", "clear this outpost's pins and redial")
	until := fs.String("until", "", "wait for connecting, connected, stalled, lost or refused")
	watch := fs.Bool("watch", false, "print every outposts_updated event until interrupted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	actions := 0
	for _, v := range []string{*add, *remove, *repair} {
		if v != "" {
			actions++
		}
	}
	if actions > 1 {
		return errors.New("choose only one of --add, --remove or --repair")
	}
	if *add != "" && *address == "" {
		return errors.New("--add requires --address")
	}
	switch *until {
	case "", "connecting", "connected", "stalled", "lost", "refused":
	default:
		return fmt.Errorf("unknown outpost state %q", *until)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(), nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(-1)
	data, err := hello(ctx, conn)
	if err != nil {
		return err
	}
	var initial protocol.InitialStateMessage
	if err := json.Unmarshal(data, &initial); err != nil {
		return err
	}
	if initial.Settings["outposts_enabled"] != "true" {
		return errors.New("outposts_enabled is off")
	}
	snapshot := protocol.OutpostsUpdatedMessage{Event: protocol.EventOutpostsUpdated, Outposts: initial.Outposts}
	if snapshot.Outposts == nil {
		snapshot.Outposts = []protocol.OutpostInfo{}
	}
	if actions == 0 {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if done, err := report(data, snapshot, *watch, *remove, *until, *add, *repair); done || err != nil {
			return err
		}
	} else {
		var command any
		switch {
		case *add != "":
			command = protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: *add, Address: *address}
		case *remove != "":
			command = protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: *remove}
		default:
			command = protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: *repair}
		}
		data, err := json.Marshal(command)
		if err != nil {
			return err
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return err
		}
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var event struct {
			Event string `json:"event"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		if event.Event == protocol.EventCommandError {
			return errors.New(event.Error)
		}
		if event.Event != protocol.EventOutpostsUpdated {
			continue
		}
		var message protocol.OutpostsUpdatedMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return err
		}
		if done, err := report(data, message, *watch, *remove, *until, *add, *repair); done || err != nil {
			return err
		}
	}
}

func report(data []byte, message protocol.OutpostsUpdatedMessage, watch bool, remove, until, add, repair string) (bool, error) {
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		return true, err
	}
	if watch {
		return false, nil
	}
	if remove != "" {
		for _, row := range message.Outposts {
			if row.ID == remove {
				return false, nil
			}
		}
		return true, nil
	}
	if until != "" || add != "" || repair != "" {
		for _, row := range message.Outposts {
			if add != "" && row.Name != add || repair != "" && row.ID != repair {
				continue
			}
			if until == "" || string(row.State) == until {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}
