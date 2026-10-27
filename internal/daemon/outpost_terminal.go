package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/crypto/ssh"
)

const bridgeCall = "call"

func (d *Daemon) callBridgeHandler(host string) func(*bridge.Conn, ssh.NewChannel) {
	order := &terminalOrder{}
	return func(conn *bridge.Conn, incoming ssh.NewChannel) {
		var header struct {
			Cmd string `json:"cmd"`
		}
		if err := json.Unmarshal(bridge.CallHeader(incoming), &header); err != nil {
			_ = incoming.Reject(ssh.Prohibited, err.Error())
			return
		}
		part, ok := d.router().here[header.Cmd]
		if !ok {
			_ = incoming.Reject(ssh.UnknownChannelType, fmt.Sprintf("no machine part runs %s", header.Cmd))
			return
		}
		msg, err := part.decode(bridge.CallHeader(incoming))
		if err != nil {
			_ = incoming.Reject(ssh.Prohibited, err.Error())
			return
		}
		execute := func() {
			bridge.Answer(incoming, func(ctx context.Context, raw []byte) (any, error) {
				full, err := part.decode(raw)
				if err != nil {
					return nil, err
				}
				if host != "" {
					if !d.isOutpostConn(host, conn) {
						return nil, fmt.Errorf("outpost bridge was replaced")
					}
					row, err := d.resolveOutpost(host)
					if err != nil {
						return nil, err
					}
					ctx = context.WithValue(ctx, originKey{}, Origin{Host: host, Name: row.Name, bridge: conn})
				}
				if host != "" {
					return Call[any](ctx, d.router(), full)
				}
				return part.run(ctx, full)
			})
		}
		if msg.Placement().Locator == protocol.LocatorTerminal && !part.readOnly {
			terminal := protocol.TerminalID(msg.Locate())
			if !order.queue(terminal, execute) {
				return
			}
			if !d.life.Go("bridgeTerminalOrder", func() { order.runQueued(terminal) }) {
				order.runQueued(terminal)
			}
		} else if !d.life.Go("bridgeTerminal", execute) {
			_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
		}
	}
}

func (d *Daemon) requireSessionHost(id protocol.SessionID) error {
	if host := d.sessionHost(id); host != "" {
		_, _, err := d.outpostConn(host)
		return err
	}
	return nil
}

func (d *Daemon) unreachableSessionHost(id protocol.SessionID) string {
	if host := d.sessionHost(id); host != "" {
		if _, _, err := d.outpostConn(host); err != nil {
			return host
		}
	}
	return ""
}

func (d *Daemon) requireLiveSessionHost(id protocol.SessionID) error {
	if d.sessionLive(context.Background(), id) {
		return d.requireSessionHost(id)
	}
	return nil
}

func (d *Daemon) outpostName(host string) string {
	if row, err := d.resolveOutpost(host); err == nil {
		return row.Name
	}
	return host
}

func (d *Daemon) sendOutpostTheme(ctx context.Context, host string) error {
	theme := d.currentTerminalTheme()
	_, err := Call[struct{}](ctx, d.router(), protocol.TerminalThemeMessage{Cmd: protocol.CmdTerminalTheme, Host: host, Foreground: theme.Foreground, Background: theme.Background, Cursor: theme.Cursor, AnsiPalette: theme.ANSIPalette[:]})
	return err
}

func (d *Daemon) isOutpostConn(host string, conn *bridge.Conn) bool {
	d.outposts.mu.Lock()
	defer d.outposts.mu.Unlock()
	run := d.outposts.dialers[host]
	return run != nil && run.conn == conn
}
