package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) terminalCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"terminal ls":    command.Typed(d.runTerminalList),
		"terminal open":  command.Typed(d.runTerminalOpen),
		"terminal run":   command.Typed(d.runTerminalRun),
		"terminal send":  command.Typed(d.runTerminalSend),
		"terminal read":  command.Typed(d.runTerminalRead),
		"terminal wait":  command.Typed(d.runTerminalWait),
		"terminal move":  command.Typed(d.runTerminalMove),
		"terminal close": command.Typed(d.runTerminalClose),
	}
}

func terminalDeadline(ctx context.Context, timeout string) (context.Context, context.CancelFunc, error) {
	if timeout == "" {
		ctx, cancel := context.WithCancel(ctx)
		return ctx, cancel, nil
	}
	seconds, err := time.ParseDuration(timeout + "s")
	if err != nil || seconds <= 0 {
		return nil, nil, command.Usage("{timeout} must be positive seconds; got %q", timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, seconds)
	return ctx, cancel, nil
}

func timedOut(ctx context.Context, err error) error {
	if deadline, bounded := ctx.Deadline(); err != nil && bounded && !time.Now().Before(deadline) {
		return command.Refuse(command.CodeTimeout, "context deadline exceeded")
	}
	return err
}

func (d *Daemon) runTerminalRun(ctx context.Context, c command.Caller, a command.TerminalRunArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	ctx, cancel, err := terminalDeadline(ctx, a.Timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	input, target, err := d.inputTerminal(ctx, caller, terminalInput{target: a.Terminal, desktop: a.Desktop, data: a.Command + "\r", run: true, wait: a.Wait})
	if err != nil {
		return nil, timedOut(ctx, err)
	}
	if !a.Wait {
		return protocol.TerminalRunResult{TerminalID: protocol.Ptr(input.TerminalID), LastBlockID: input.LastBlockID}, nil
	}
	waited, err := d.waitTerminal(ctx, target, protocol.TerminalWaitUntilPrompt, input.LastBlockID)
	if err != nil {
		return nil, timedOut(ctx, err)
	}
	return protocol.TerminalRunResult{Block: waited.Block, Exited: waited.Exited, ExitCode: waited.ExitCode}, nil
}

func (d *Daemon) runTerminalSend(ctx context.Context, c command.Caller, a command.TerminalSendArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	if (a.Text == nil) == (len(a.Keys) == 0) {
		return nil, command.Usage("send needs exactly one of {text} or {keys}")
	}
	data := protocol.Deref(a.Text)
	if len(a.Keys) > 0 {
		if data, err = keyBytes(a.Keys); err != nil {
			return nil, err
		}
	}
	input, _, err := d.inputTerminal(ctx, caller, terminalInput{target: a.Terminal, desktop: a.Desktop, data: data})
	if err != nil {
		return nil, err
	}
	return input, nil
}

func (d *Daemon) runTerminalWait(ctx context.Context, c command.Caller, a command.TerminalWaitArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	until := protocol.TerminalWaitUntil(a.Until)
	if until != protocol.TerminalWaitUntilPrompt && until != protocol.TerminalWaitUntilExit {
		return nil, command.Usage("{until} must be prompt or exit")
	}
	ctx, cancel, err := terminalDeadline(ctx, a.Timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	target, err := d.terminalTarget(caller, a.Terminal)
	if err != nil {
		return nil, err
	}
	waited, err := d.waitTerminal(ctx, target, until, nil)
	if err != nil {
		return nil, timedOut(ctx, err)
	}
	return waited, nil
}

var terminalKeyNames = map[string]string{
	"escape": "\x1b", "enter": "\r", "tab": "\t", "backspace": "\x7f", "space": " ",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F", "insert": "\x1b[2~", "delete": "\x1b[3~",
	"pageup": "\x1b[5~", "pagedown": "\x1b[6~",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

func keyBytes(keys []string) (string, error) {
	var out strings.Builder
	for _, key := range keys {
		lower := strings.ToLower(key)
		if bytes, ok := terminalKeyNames[lower]; ok {
			out.WriteString(bytes)
			continue
		}
		if len(lower) == 3 && strings.HasPrefix(lower, "c-") {
			switch control := lower[2]; {
			case control >= 'a' && control <= 'z':
				out.WriteByte(control - 'a' + 1)
				continue
			case control >= '\\' && control <= '_':
				out.WriteByte(control & 31)
				continue
			}
		}
		return "", command.Refuse(command.CodeUnknownKey, "unknown key %q; the keys are C-a through C-z, C-\\, C-], C-^, C-_, Escape, Enter, Tab, Backspace, Space, Up, Down, Left, Right, Home, End, Delete, Insert, PageUp, PageDown and F1 through F12", key)
	}
	return out.String(), nil
}
