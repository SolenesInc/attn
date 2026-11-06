package diag

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

func ReadProcess(pid int) (Process, bool) {
	statm, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return Process{}, false
	}
	var size, resident uint64
	if _, err := fmt.Sscan(string(statm), &size, &resident); err != nil {
		return Process{}, false
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return Process{}, false
	}
	return Process{PID: pid, Name: string(bytes.TrimSpace(comm)), ResidentBytes: resident * uint64(os.Getpagesize())}, true
}

func (r RoleMemories) AddWebKitProcesses(appPID int) {
	stats, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		return
	}
	children := map[int][]int{}
	for _, path := range stats {
		pid, parent, ok := parseProcStat(path)
		if ok {
			children[parent] = append(children[parent], pid)
		}
	}
	queue := slices.Clone(children[appPID])
	for len(queue) > 0 {
		pid := queue[0]
		queue = append(queue[1:], children[pid]...)
		process, ok := ReadProcess(pid)
		if !ok {
			continue
		}
		if role, isWebKit := webKitRole(process.Name); isWebKit {
			r.Add(role, process)
		}
	}
}

func parseProcStat(path string) (pid, parent int, ok bool) {
	stat, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	closing := bytes.LastIndexByte(stat, ')')
	if closing < 0 {
		return 0, 0, false
	}
	fields := bytes.Fields(stat[closing+1:])
	if len(fields) < 2 {
		return 0, 0, false
	}
	pid, err = strconv.Atoi(string(bytes.Fields(stat)[0]))
	if err != nil {
		return 0, 0, false
	}
	parent, err = strconv.Atoi(string(fields[1]))
	return pid, parent, err == nil
}
