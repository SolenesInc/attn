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
	status := ProgramStatus{
		State:   ProgramStatusState(report.state),
		Kind:    ProgramStatusKind(report.kind),
		ID:      ghosttyString(report.id),
		Message: ghosttyString(report.message),
	}
	s.mu.Lock()
	s.programStatus = append(s.programStatus, status)
	s.mu.Unlock()
}

func ghosttyString(s C.GhosttyString) string {
	if s.len == 0 || s.ptr == nil {
		return ""
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(s.ptr)), C.int(s.len))
}
