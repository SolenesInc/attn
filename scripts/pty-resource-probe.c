#include <errno.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <termios.h>
#include <unistd.h>
#ifdef __APPLE__
#include <libproc.h>
#include <mach/mach_time.h>
#include <sys/resource.h>
#endif

static void write_all(int fd, const void *data, size_t length) {
    const char *bytes = data;
    while (length) {
        ssize_t count = write(fd, bytes, length);
        if (count < 0 && errno == EINTR) continue;
        if (count <= 0) exit(2);
        bytes += count;
        length -= (size_t)count;
    }
}

static int child(const char *socket_path, const char *id) {
    struct termios mode;
    if (tcgetattr(STDIN_FILENO, &mode)) return 2;
    cfmakeraw(&mode);
    if (tcsetattr(STDIN_FILENO, TCSANOW, &mode)) return 2;
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    struct sockaddr_un addr = {.sun_family = AF_UNIX};
    if (strlen(socket_path) >= sizeof(addr.sun_path)) return 2;
    strcpy(addr.sun_path, socket_path);
    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr))) return 2;
    dprintf(fd, "%s\n", id);
    char command[128], block[4096];
    memset(block, 'x', sizeof(block));
    while (fgets(command, sizeof(command), stdin)) {
        unsigned count = 0;
        if (sscanf(command, "flood %u", &count) != 1) return 2;
        for (unsigned left = count; left;) {
            size_t chunk = left < sizeof(block) ? left : sizeof(block);
            write_all(STDOUT_FILENO, block, chunk);
            left -= (unsigned)chunk;
        }
        write_all(STDOUT_FILENO, "PERF_DONE\033[5n", 13);
        char reply[4];
        if (fread(reply, 1, sizeof(reply), stdin) != sizeof(reply) || memcmp(reply, "\033[0n", 4)) return 3;
        write_all(fd, "done\n", 5);
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc == 4 && !strcmp(argv[1], "--child")) return child(argv[2], argv[3]);
    if (argc != 2) return 2;
#ifdef __APPLE__
    int pid = atoi(argv[1]);
    struct rusage_info_v4 usage = {0};
    struct proc_taskinfo task = {0};
    if (pid <= 0 || proc_pid_rusage(pid, RUSAGE_INFO_V4, (rusage_info_t *)&usage)) {
        perror("proc_pid_rusage");
        return 1;
    }
    if (proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &task, sizeof(task)) != sizeof(task)) return 1;
    mach_timebase_info_data_t timebase;
    if (mach_timebase_info(&timebase)) return 1;
    uint64_t cpu_ns = (uint64_t)(((__uint128_t)usage.ri_user_time + usage.ri_system_time) * timebase.numer / timebase.denom);
    printf("{\"physical_bytes\":%" PRIu64 ",\"resident_bytes\":%" PRIu64 ",\"cpu_ns\":%" PRIu64 ",\"instructions\":%" PRIu64 ",\"threads\":%d}\n",
           usage.ri_phys_footprint, usage.ri_resident_size, cpu_ns, usage.ri_instructions, task.pti_threadnum);
    return 0;
#else
    fputs("physical footprint sampling requires macOS\n", stderr);
    return 2;
#endif
}
