//go:build linux && cgo

package main

/*
#include <malloc.h>

__attribute__((constructor)) static void attn_tune_c_allocator(void) {
	mallopt(M_ARENA_MAX, 2);
	mallopt(M_MMAP_THRESHOLD, 128 << 10);
}
*/
import "C"
