//go:build darwin && cgo

package diag

/*
#include <libproc.h>
#include <sys/resource.h>
#include <sys/types.h>

extern pid_t responsibility_get_pid_responsible_for_pid(pid_t) __attribute__((weak_import));

static pid_t attn_responsible_pid(pid_t pid) {
	return responsibility_get_pid_responsible_for_pid ? responsibility_get_pid_responsible_for_pid(pid) : -1;
}

static int attn_process_memory(pid_t pid, unsigned long long *footprint, unsigned long long *resident) {
	struct rusage_info_v4 info;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&info) != 0) {
		return -1;
	}
	*footprint = info.ri_phys_footprint;
	*resident = info.ri_resident_size;
	return 0;
}
*/
import "C"

import "unsafe"

// kern.maxproc is 8000 on the Mac this was measured on, with 1021 running.
const maxProcesses = 16384

func ReadProcess(pid int) (Process, bool) {
	var footprint, resident C.ulonglong
	if C.attn_process_memory(C.pid_t(pid), &footprint, &resident) != 0 {
		return Process{}, false
	}
	return Process{PID: pid, Name: processName(pid), FootprintBytes: uint64(footprint), ResidentBytes: uint64(resident)}, true
}

func processName(pid int) string {
	var name [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	if C.proc_name(C.int(pid), unsafe.Pointer(&name[0]), C.uint32_t(len(name))) <= 0 {
		return ""
	}
	return C.GoString(&name[0])
}

func (r RoleMemories) AddWebKitProcesses(appPID int) {
	var pids [maxProcesses]C.int
	count := C.proc_listallpids(unsafe.Pointer(&pids[0]), C.int(unsafe.Sizeof(pids)))
	if count <= 0 {
		return
	}
	for _, pid := range pids[:count] {
		if int(pid) == appPID || int(C.attn_responsible_pid(C.pid_t(pid))) != appPID {
			continue
		}
		role, isWebKit := webKitRole(processName(int(pid)))
		if !isWebKit {
			continue
		}
		if process, ok := ReadProcess(int(pid)); ok {
			r.Add(role, process)
		}
	}
}
