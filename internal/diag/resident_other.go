//go:build !linux && !(darwin && cgo)

package diag

func residentBytes() uint64 { return 0 }
