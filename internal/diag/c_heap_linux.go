//go:build linux && cgo

package diag

/*
#include <malloc.h>

#pragma GCC diagnostic ignored "-Wdeprecated-declarations"
static struct mallinfo attn_mallinfo(void) { return mallinfo(); }
*/
import "C"

func cHeap() (inUse, free uint64) {
	info := C.attn_mallinfo()
	return uint64(uint32(info.uordblks)) + uint64(uint32(info.hblkhd)), uint64(uint32(info.fordblks))
}
