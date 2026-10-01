# Resume availability scan receipt

Measured on macOS on 2026-10-01 with Bun, reading session headers until the
first valid JSON record. Missing IDs force a full directory scan. Real Pi
storage was read-only; synthetic files were created in temporary directories.

Initial discovery experiment (five scans per case):

| Directory | Serial stream maximum | Ten concurrent streams maximum |
| --- | --- | --- |
| Largest local Pi cwd directory, 91 files | 64.12 ms | 125.20 ms |
| Synthetic directory, 10,000 header-only files | 9,540.15 ms | 4,032.70 ms |

Repeated batch scans under verification load were substantially slower with streams.
The implementation now explicitly opens, reads, and closes each header file,
with ten concurrent reads, matching pinned Pi 0.83.0's discovery concurrency.
Its 4,096-byte read buffer also matches Pi's header reader; it imposes no
header-size limit. A multibyte header spanning buffers is in the storage corpus.

Batch measurements (three scans per case in each run):

| Request | Explicit reader experiment maximum | Final production implementation maximum |
| --- | --- | --- |
| One missing ID, shared directory with 10,000 files | 4,986.95 ms | 5,535.64 ms |
| Ten missing IDs, two cwds sharing that directory | 1,283.54 ms | 5,289.79 ms |
| Ten missing IDs, two default directories, 10,000 total files | 7,254.60 ms | 4,886.54 ms |

Garden review sends one batch per plugin per capture. Pi scans each resolved
storage directory once, including custom storage shared across cwds. Each
capture reads fresh storage; deletion is visible on the next read. There is no
persistent cache. The daemon's dedicated ten-second availability deadline is
above the measured healthy stress batches. Timeout failures name the limit.

Raw measurement scripts and outputs are attached to Garden seed `s-g2jn71`.
These are local receipts, not latency guarantees on other filesystems or for
unbounded collections of distinct storage directories.
