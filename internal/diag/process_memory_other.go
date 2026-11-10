//go:build !linux && !(darwin && cgo)

package diag

import "context"

func ReadProcess(int) (Process, bool) { return Process{}, false }

func (RoleMemories) AddWebKitProcesses(int) {}

func (RoleMemories) SplitWebContent(context.Context) error { return nil }
