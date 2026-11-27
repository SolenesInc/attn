package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const machineWSPath = "/ws/machine"

var machineLinkCommands = map[string]bool{
	protocol.CmdClientHello:             true,
	protocol.CmdAttachSession:           true,
	protocol.CmdDetachSession:           true,
	protocol.CmdPtyInput:                true,
	protocol.CmdPtyResize:               true,
	protocol.CmdTerminalFocus:           true,
	protocol.CmdTerminalPointerActivity: true,
	protocol.CmdGetScreenSnapshot:       true,
	protocol.CmdGetKittyImage:           true,
	protocol.CmdGetSettings:             true,
	protocol.CmdSetSetting:              true,
}

func machineLinkRefusal(cmd string, msg any) error {
	if !machineLinkCommands[cmd] {
		return fmt.Errorf("%s carries only this machine's terminals and settings; send %s to the home", machineWSPath, cmd)
	}
	switch m := msg.(type) {
	case *protocol.AttachSessionMessage:
		if protocol.Deref(m.AttachPolicy) == protocol.AttachPolicyRevive {
			return fmt.Errorf("reviving a session starts it again, which the home does; send it to the home")
		}
	case *protocol.SetSettingMessage:
		if spec, ok := lookupSetting(m.Key); !ok || spec.Class != setting.Machine {
			return fmt.Errorf("%s is not a setting of this machine; set it at the home", m.Key)
		}
	}
	return nil
}
