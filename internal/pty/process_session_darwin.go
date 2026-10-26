package pty

import "golang.org/x/sys/unix"

func sessionProcesses(sid int) ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var members []int
	for _, process := range processes {
		pid := int(process.Proc.P_pid)
		if pid > 0 {
			if actual, err := unix.Getsid(pid); err == nil && actual == sid {
				members = append(members, pid)
			}
		}
	}
	return members, nil
}
