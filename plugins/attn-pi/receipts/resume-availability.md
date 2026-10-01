# Resume availability scan receipt

Measured on macOS on 2026-10-01 with Bun, reading session headers until the
first valid JSON record. Missing IDs force a full directory scan. Real Pi
storage was read-only; synthetic files were created in a temporary directory.

| Directory | Serial scan maximum | Ten concurrent reads maximum |
| --- | --- | --- |
| Largest local Pi cwd directory, 91 files, five scans | 64.12 ms | 125.20 ms |
| Synthetic directory, 10,000 header-only files, five scans | 9,540.15 ms | 4,032.70 ms |

Ten concurrent reads matches pinned Pi 0.83.0's session discovery concurrency.
The daemon uses a dedicated ten-second availability deadline, above the
measured healthy stress case. A Garden review capture pays that deadline once
per stalled plugin; a later capture retries. Timeout failures name the limit.

Raw measurement scripts and outputs are attached to Garden seed `s-g2jn71`.
This is a local measurement, not a latency guarantee on other filesystems.
