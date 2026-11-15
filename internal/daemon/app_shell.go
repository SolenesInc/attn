package daemon

import "github.com/victorarias/attn/internal/protocol"

type appShell string

const (
	shellNone   appShell = ""
	shellTauri  appShell = "tauri"
	shellNative appShell = "native"
)

func appShellOf(clientKind string) appShell {
	switch clientKind {
	case protocol.ClientKindTauriApp:
		return shellTauri
	case protocol.ClientKindNativeApp:
		return shellNative
	}
	return shellNone
}
