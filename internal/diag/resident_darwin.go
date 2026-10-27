//go:build darwin && cgo

package diag

/*
#include <mach/mach.h>

static unsigned long long attn_resident_bytes(void) {
	mach_task_basic_info_data_t info;
	mach_msg_type_number_t count = MACH_TASK_BASIC_INFO_COUNT;
	if (task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&info, &count) != KERN_SUCCESS) {
		return 0;
	}
	return info.resident_size;
}
*/
import "C"

func residentBytes() uint64 {
	return uint64(C.attn_resident_bytes())
}
