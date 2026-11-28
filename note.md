# Each window's clips get files of their own

The app writes a view's clip to a temporary file for Rune's player, and numbered those files per window inside one folder per process. Two windows in one process both wrote `1.wav`, so when one stopped drawing its clip it deleted the other's file. That clip then showed "no such file" or "no playable audio track". The count is now per process.

It showed as the macOS view clip tests failing in 7 of 10 runs of `cargo test -p attn-app --test wire views::`, because parallel test worlds share a process. With the fix, 10 of 10 pass.
