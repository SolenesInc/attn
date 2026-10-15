package pty

import "golang.org/x/sys/unix"

func foregroundProcessName(pid int) string {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	return unix.ByteSliceToString(p.Proc.P_comm[:])
}
