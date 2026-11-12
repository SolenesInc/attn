//go:build !(linux && cgo)

package diag

func cHeap() (inUse, free uint64) { return 0, 0 }
