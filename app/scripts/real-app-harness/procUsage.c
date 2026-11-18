// One line per readable process: pid ppid responsible_pid phys_footprint_bytes cpu_ns
#include <libproc.h>
#include <mach/mach_time.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>

extern pid_t responsibility_get_pid_responsible_for_pid(pid_t) __attribute__((weak_import));

int main(void) {
  mach_timebase_info_data_t timebase;
  mach_timebase_info(&timebase);
  int count = proc_listallpids(NULL, 0);
  pid_t *pids = calloc(count * 2, sizeof(pid_t));
  count = proc_listallpids(pids, count * 2 * sizeof(pid_t));
  for (int i = 0; i < count; i++) {
    struct rusage_info_v4 usage;
    struct proc_bsdinfo info;
    if (proc_pid_rusage(pids[i], RUSAGE_INFO_V4, (rusage_info_t *)&usage) != 0) continue;
    if (proc_pidinfo(pids[i], PROC_PIDTBSDINFO, 0, &info, sizeof info) != sizeof info) continue;
    pid_t responsible = responsibility_get_pid_responsible_for_pid ? responsibility_get_pid_responsible_for_pid(pids[i]) : -1;
    unsigned long long cpu = (usage.ri_user_time + usage.ri_system_time) * timebase.numer / timebase.denom;
    printf("%d %d %d %llu %llu\n", pids[i], info.pbi_ppid, responsible, usage.ri_phys_footprint, cpu);
  }
  return 0;
}
