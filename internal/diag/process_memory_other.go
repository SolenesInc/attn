//go:build !linux && !(darwin && cgo)

package diag

func ReadProcess(int) (Process, bool) { return Process{}, false }

func (RoleMemories) AddWebKitProcesses(int) {}
