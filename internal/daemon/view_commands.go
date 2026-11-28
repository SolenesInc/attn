package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/viewlang/catalog"
)

// viewCommandKey is one control's run: a command, the part that offers it and
// the rows it is in.
type viewCommandKey struct {
	command string
	part    int
	rows    string
}

func keyOfViewCommand(command protocol.ViewCommand) viewCommandKey {
	return viewCommandKey{command: command.Command, part: command.Part, rows: strings.Join(command.Rows, "\x00")}
}

func (d *Daemon) handleViewRun(client *wsClient, msg *protocol.ViewRunMessage) {
	result := protocol.ViewRunResultMessage{Event: protocol.EventViewRunResult, RequestID: msg.RequestID}
	if err := d.runView(client.selectedProfile(), msg); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

// runView starts the command a control of a tile's view names, from the row
// the control is in, and keeps its phase on the tile until it ends.
func (d *Daemon) runView(profileID string, msg *protocol.ViewRunMessage) error {
	tile, err := d.shownViewTile(profileID, msg.DesktopID, msg.TileID)
	if err != nil {
		return err
	}
	view := d.heldView(tile.document, profileID)
	if view == nil {
		return fmt.Errorf("%s is not shown as a view", tile.document.path)
	}
	run, err := viewCommandOf(view, msg)
	if err != nil {
		return err
	}
	ran := protocol.ViewCommand{Command: msg.Command, Part: msg.Part, Rows: msg.Rows, Phase: protocol.ViewCommandPhaseBusy}
	if !d.setViewCommandPhase(tile.document, ran, true) {
		return fmt.Errorf("%s is still running", msg.Command)
	}
	d.life.Go("viewRun", func() {
		if err := run(d.life.Context(), d, tile.document); err != nil {
			ran.Phase, ran.Error = protocol.ViewCommandPhaseFailed, protocol.Ptr(err.Error())
		} else {
			ran.Phase = protocol.ViewCommandPhaseDone
		}
		d.setViewCommandPhase(tile.document, ran, false)
	})
	return nil
}

// viewCommandRun is a view command a control asked for, ready to run against
// the document of the view.
type viewCommandRun func(ctx context.Context, d *Daemon, document tileDocument) error

// viewCommandOf is the run of the command msg names: one the view defines,
// with its arguments filled from the row the control is in.
func viewCommandOf(view *protocol.ViewDocument, msg *protocol.ViewRunMessage) (viewCommandRun, error) {
	own := ownViewCommand(view.Parts, msg.Command)
	if own == nil {
		if slices.ContainsFunc(catalog.Commands, func(c catalog.Named) bool { return c.Name == msg.Command }) {
			return nil, fmt.Errorf("attn's %s does not run from a view yet", msg.Command)
		}
		return nil, fmt.Errorf("the view defines no command %s", msg.Command)
	}
	i := slices.IndexFunc(view.Commands, func(c protocol.ViewRunCommand) bool { return c.Line == own.Written })
	if i < 0 || view.Commands[i].State != protocol.ViewRunStateStarted {
		return nil, fmt.Errorf("%s runs once you start the view's commands: press r on its tile", own.Name)
	}
	scope, found := viewCallScope(view.Parts, newViewScope(view), msg.Part, msg.Rows)
	if !found {
		return nil, fmt.Errorf("the row %s ran from is no longer shown", own.Name)
	}
	args := make([]string, 0, len(own.Args)+1)
	for _, arg := range own.Args {
		args = append(args, scope.text(arg))
	}
	if msg.Text != nil {
		args = append(args, *msg.Text)
	}
	run := protocol.ViewCommandRunMessage{Cmd: protocol.CmdViewCommandRun, Line: own.Written, Args: args}
	return func(ctx context.Context, d *Daemon, document tileDocument) error {
		run.Path = document.path
		if document.host != "" {
			run.Host = protocol.Ptr(document.host)
		}
		_, err := Call[struct{}](ctx, d.router(), run)
		return d.tileDocumentError(document.host, err)
	}, nil
}

func ownViewCommand(parts []protocol.ViewPart, name string) *protocol.ViewOwnCommand {
	var found *protocol.ViewOwnCommand
	eachViewPart(parts, func(part protocol.ViewPart) {
		if part.Commands == nil || found != nil {
			return
		}
		for i := range part.Commands.Commands {
			if part.Commands.Commands[i].Name == name {
				found = &part.Commands.Commands[i]
			}
		}
	})
	return found
}

// viewCallScope is where the part on line reads its fields in the rows whose
// ids are rows, outermost first: the items of the each and one-by-one parts
// around it, then the row of its own list.
func viewCallScope(parts []protocol.ViewPart, scope viewScope, line int, rows []string) (viewScope, bool) {
	for _, part := range parts {
		if part.Line == line {
			if part.List == nil || len(rows) == 0 {
				return scope, len(rows) == 0
			}
			return scope.row(part.List.Items, rows)
		}
		var items *protocol.ViewItems
		var blocks []protocol.ViewPart
		switch {
		case part.Each != nil:
			items, blocks = &part.Each.Items, part.Each.Blocks
		case part.OneByOne != nil:
			items, blocks = &part.OneByOne.Items, part.OneByOne.Blocks
		default:
			continue
		}
		if !viewPartHolds(blocks, line) {
			continue
		}
		if len(rows) == 0 {
			return scope, false
		}
		inner, found := scope.row(*items, rows[:1])
		if !found {
			return scope, false
		}
		return viewCallScope(blocks, inner, line, rows[1:])
	}
	return scope, false
}

func viewPartHolds(parts []protocol.ViewPart, line int) bool {
	held := false
	eachViewPart(parts, func(part protocol.ViewPart) { held = held || part.Line == line })
	return held
}

// row is the scope inside the item of items whose id is the one row.
func (s viewScope) row(items protocol.ViewItems, rows []string) (viewScope, bool) {
	if len(rows) != 1 {
		return s, false
	}
	for n, item := range s.items(items) {
		if viewRowID(item, n) == rows[0] {
			return s.within(item), true
		}
	}
	return s, false
}

// text is template with each field's value in its place, as a program reads
// it: text as it is, a number as written, true or false, and other values as
// JSON.
func (s viewScope) text(template []protocol.ViewTemplatePiece) string {
	var out strings.Builder
	for _, piece := range template {
		if piece.Text != nil {
			out.WriteString(*piece.Text)
			continue
		}
		switch value := s.field(piece.Field).(type) {
		case nil:
		case string:
			out.WriteString(value)
		case float64:
			out.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
		case bool:
			out.WriteString(strconv.FormatBool(value))
		default:
			encoded, _ := json.Marshal(value)
			out.Write(encoded)
		}
	}
	return out.String()
}

// setViewCommandPhase keeps how a control's run stands on the document that
// shows it, and sends it to the windows showing it. A busy run cannot start
// again while it is busy, so starting reports false then.
func (d *Daemon) setViewCommandPhase(document tileDocument, ran protocol.ViewCommand, starting bool) bool {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	held := delivery.documents[document]
	if held == nil {
		delivery.mu.Unlock()
		return !starting
	}
	key := keyOfViewCommand(ran)
	i := slices.IndexFunc(held.commands, func(c protocol.ViewCommand) bool { return keyOfViewCommand(c) == key })
	if starting && i >= 0 && held.commands[i].Phase == protocol.ViewCommandPhaseBusy {
		delivery.mu.Unlock()
		return false
	}
	if i >= 0 {
		held.commands[i] = ran
	} else {
		held.commands = append(held.commands, ran)
	}
	held.serial++
	delivery.mu.Unlock()
	d.nudgeDesktopTileContent()
	return true
}

// runOwnViewCommand runs a view's own command with the arguments the home
// filled, once the user started it under the line and script it has now.
func (d *Daemon) runOwnViewCommand(ctx context.Context, msg protocol.ViewCommandRunMessage) (struct{}, error) {
	read := d.readViewCommands(msg.Path)
	lines := make([]string, 0, len(read))
	for _, r := range read {
		lines = append(lines, r.command.line)
	}
	i := slices.IndexFunc(read, func(r viewCommandRead) bool { return r.command.own != nil && r.command.line == msg.Line })
	if i < 0 {
		return struct{}{}, errors.New("the view no longer defines this command")
	}
	r := read[i]
	folder := filepath.Dir(msg.Path)
	d.viewRuns.mu.Lock()
	started := d.viewRuns.started[msg.Path]
	d.viewRuns.mu.Unlock()
	if state, change := viewCommandState(folder, r.command, r.files, r.script, started, lines); state != protocol.ViewRunStateStarted {
		said := fmt.Sprintf("%s runs once you start the view's commands", r.command.own.Name)
		if change != nil {
			said = fmt.Sprintf("%s %s", r.command.own.Name, *change)
		}
		return struct{}{}, errors.New(said)
	}
	return struct{}{}, runViewProgram(ctx, folder, r.command.own.Name, append([]string{r.command.own.Program}, msg.Args...), io.Discard)
}
