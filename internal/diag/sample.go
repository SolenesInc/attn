package diag

import (
	"os"
	"runtime"
	"syscall"
	"time"
)

type ProcessSample struct {
	Goroutines    int    `json:"goroutines"`
	ResidentBytes uint64 `json:"resident_bytes"`
	CPUTimeMS     int64  `json:"cpu_time_ms"`
	OpenFDs       int    `json:"open_fds"`
	HeapAlloc     uint64 `json:"heap_alloc"`
	HeapSys       uint64 `json:"heap_sys"`
	HeapIdle      uint64 `json:"heap_idle"`
	HeapInuse     uint64 `json:"heap_inuse"`
	HeapReleased  uint64 `json:"heap_released"`
	HeapObjects   uint64 `json:"heap_objects"`
	Sys           uint64 `json:"sys"`
	NextGC        uint64 `json:"next_gc"`
	NumGC         uint32 `json:"num_gc"`
	GCPauseTotal  uint64 `json:"gc_pause_total_ns"`
}

func Sample() ProcessSample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ProcessSample{
		Goroutines:    runtime.NumGoroutine(),
		ResidentBytes: residentBytes(),
		CPUTimeMS:     cpuTime().Milliseconds(),
		OpenFDs:       openFDs(),
		HeapAlloc:     ms.HeapAlloc,
		HeapSys:       ms.HeapSys,
		HeapIdle:      ms.HeapIdle,
		HeapInuse:     ms.HeapInuse,
		HeapReleased:  ms.HeapReleased,
		HeapObjects:   ms.HeapObjects,
		Sys:           ms.Sys,
		NextGC:        ms.NextGC,
		NumGC:         ms.NumGC,
		GCPauseTotal:  ms.PauseTotalNs,
	}
}

func cpuTime() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func openFDs() int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0
	}
	return len(entries) - 1
}
