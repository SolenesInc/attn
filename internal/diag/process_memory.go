package diag

import (
	"slices"
	"strings"
)

const (
	RoleDaemon           = "daemon"
	RolePTYWorker        = "pty_worker"
	RolePTYHost          = "pty_host"
	RoleApp              = "app"
	RoleWebKitContent    = "webkit_content"
	RoleWebKitGPU        = "webkit_gpu"
	RoleWebKitNetworking = "webkit_networking"
)

type Process struct {
	PID            int
	Name           string
	FootprintBytes uint64
	ResidentBytes  uint64
}

type RoleMemory struct {
	Role           string           `json:"role"`
	Shell          string           `json:"shell,omitempty"`
	Count          int              `json:"count"`
	ResidentBytes  uint64           `json:"resident_bytes"`
	FootprintBytes uint64           `json:"footprint_bytes,omitempty"`
	Split          *WebContentSplit `json:"split,omitempty"`
	pids           []int
}

type WebContentSplit struct {
	WebKitMallocBytes  uint64 `json:"webkit_malloc_bytes"`
	OwnedGraphicsBytes uint64 `json:"owned_graphics_bytes"`
	GigacageBytes      uint64 `json:"gigacage_bytes"`
}

func (m *RoleMemory) add(process Process) {
	m.Count++
	m.pids = append(m.pids, process.PID)
	m.ResidentBytes += process.ResidentBytes
	m.FootprintBytes += process.FootprintBytes
}

type RoleMemories map[string]*RoleMemory

func (r RoleMemories) Add(role string, process Process) {
	memory := r[role]
	if memory == nil {
		memory = &RoleMemory{Role: role}
		r[role] = memory
	}
	memory.add(process)
}

func (r RoleMemories) AddApp(shell string, process Process) {
	r.Add(RoleApp, process)
	r[RoleApp].Shell = shell
}

func (r RoleMemories) Sorted() []RoleMemory {
	sorted := make([]RoleMemory, 0, len(r))
	for _, memory := range r {
		sorted = append(sorted, *memory)
	}
	slices.SortFunc(sorted, func(a, b RoleMemory) int { return strings.Compare(a.Role, b.Role) })
	return sorted
}

func webKitRole(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "com.apple.WebKit.WebContent"), strings.HasPrefix(name, "WebKitWebProces"):
		return RoleWebKitContent, true
	case strings.HasPrefix(name, "com.apple.WebKit.GPU"), strings.HasPrefix(name, "WebKitGPUProces"):
		return RoleWebKitGPU, true
	case strings.HasPrefix(name, "com.apple.WebKit.Networking"), strings.HasPrefix(name, "WebKitNetworkPr"):
		return RoleWebKitNetworking, true
	}
	return "", false
}
