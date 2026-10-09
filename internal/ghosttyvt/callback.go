//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

/*
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

//export goWritePty
func goWritePty(term C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, length C.size_t) {
	if userdata == nil || length == 0 {
		return
	}
	s, ok := (*(*cgo.Handle)(userdata)).Value().(*respSink)
	if !ok {
		return
	}
	s.mu.Lock()
	s.buf = append(s.buf, C.GoBytes(unsafe.Pointer(data), C.int(length))...)
	s.mu.Unlock()
}

//export goProgramStatus
func goProgramStatus(term C.GhosttyTerminal, userdata unsafe.Pointer, report *C.GhosttyTerminalProgramStatus) {
	if userdata == nil || report == nil {
		return
	}
	s, ok := (*(*cgo.Handle)(userdata)).Value().(*respSink)
	if !ok {
		return
	}
	state, known := programStatusStates[report.state]
	if !known {
		return
	}
	status := ProgramStatus{
		State:   state,
		Kind:    programStatusKinds[report.kind],
		ID:      ghosttyString(report.id),
		Message: ghosttyString(report.message),
	}
	s.mu.Lock()
	s.programStatus = append(s.programStatus, status)
	s.mu.Unlock()
}

var programStatusStates = map[C.GhosttyProgramStatusState]ProgramStatusState{
	C.GHOSTTY_PROGRAM_STATUS_STATE_IDLE:    ProgramStatusIdle,
	C.GHOSTTY_PROGRAM_STATUS_STATE_WORKING: ProgramStatusWorking,
	C.GHOSTTY_PROGRAM_STATUS_STATE_DONE:    ProgramStatusDone,
	C.GHOSTTY_PROGRAM_STATUS_STATE_BLOCKED: ProgramStatusBlocked,
	C.GHOSTTY_PROGRAM_STATUS_STATE_ERROR:   ProgramStatusError,
	C.GHOSTTY_PROGRAM_STATUS_STATE_CLEAR:   ProgramStatusClear,
}

var programStatusKinds = map[C.GhosttyProgramStatusKind]ProgramStatusKind{
	C.GHOSTTY_PROGRAM_STATUS_KIND_PERMISSION: ProgramStatusKindPermission,
	C.GHOSTTY_PROGRAM_STATUS_KIND_QUESTION:   ProgramStatusKindQuestion,
	C.GHOSTTY_PROGRAM_STATUS_KIND_AUTH:       ProgramStatusKindAuth,
}

func ghosttyString(s C.GhosttyString) string {
	if s.len == 0 || s.ptr == nil {
		return ""
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(s.ptr)), C.int(s.len))
}
