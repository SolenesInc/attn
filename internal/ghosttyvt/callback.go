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

// Invoked synchronously by libghostty-vt during vt_write, so it MUST NOT call back
// into vt_write on the same terminal.

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
