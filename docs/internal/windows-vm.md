# Windows VM

attn has no Windows build. People on Windows run the Linux build of the daemon,
CLI and native app inside WSL2, and WSLg shows the app's window on the Windows
desktop. `scripts/windows-vm` makes a Windows 11 VM on a Linux machine to try
that setup. It is a one-off tool: no CI runs it, and nothing in attn depends on
it.

## Try attn on Windows

On a Linux machine with KVM and nested virtualization, such as ai-sandbox:

```sh
scripts/windows-vm create
scripts/windows-vm install
scripts/windows-vm launch
scripts/windows-vm shot /tmp/desktop.png
scripts/windows-vm stop
```

`create` downloads Microsoft's Windows 11 Enterprise evaluation and installs it
unattended in a QEMU VM. It then installs WSL from its latest GitHub release and
imports an Ubuntu 24.04 distro, `attn-ubuntu`, from Canonical's WSL root
filesystem. It leaves the VM running. When it stops after Windows answers SSH,
run it again to finish WSL. When it stops during the Windows install, run
`delete`, then `create`.

`install` copies this checkout into the distro, or the commit you name, and
builds and installs the native app on the instance `windows`. It uses the
[Linux runner](linux-runner.md) through its SSH provider, so the distro gets
the landing runner's tools. It provisions those tools only when their inputs
changed since the last `install`.

`launch` starts the installed app through WSLg. `shot` screenshots the Windows
desktop from QEMU, whatever runs inside.

`scripts/windows-vm ssh win` opens `cmd.exe` in Windows. `scripts/windows-vm
ssh wsl` opens bash in the distro as the user `attn`, who has passwordless sudo.
Both take a command. `scripts/windows-vm vnc` prints a VNC socket that shows the
desktop live. `start` starts a stopped VM, and `delete` removes its disk.

## Start WSL with `start`

Start WSL with `scripts/windows-vm start`, not with `wsl.exe` over
`scripts/windows-vm ssh win`. WSLg draws a Linux app's window through an RDP
client, `msrdc.exe`, that WSL starts in the Windows session that started WSL.
An SSH login has no desktop, so when WSL starts there, no window shows and
WSLg's compositor keeps crashing. `start` runs the scheduled task
`attn-wsl-start` in the logged-in desktop session instead. If WSL already
started from SSH, run `scripts/windows-vm ssh win wsl --shutdown`, then
`start`.

## Times and space on ai-sandbox

Measured on 2026-11-19, with 12 CPUs and 24 GiB for the VM:

| Step | Time |
| --- | --- |
| `create`: Windows install, until SSH answers | 30 to 48 min |
| `create`: WSL and the distro | 5 to 8 min |
| `install`: first provisioning | 28 min |
| `install`: build and install the native app, cargo partly warm | 15 min |
| `start` from a stopped VM | 4 to 5 min |
| `stop` | 12 s |

The VM's disk holds 14 GB of the host's disk after `create` and 26 GB after one
`install`. The ISO takes 5.1 GB more.

## Limits

- On ai-sandbox the native app does not run inside the VM. Windows runs nested
  in KVM, and the WSL2 kernel then turns XSAVE off
  (`XSAVE consistency problem` in `dmesg`), so WSL has no AVX. The app dies
  with SIGILL (seed `s-xz170t`), and its shader can't build on lavapipe
  (`s-tfke2p`). The daemon, the PTY host and shell sessions work. Seed
  `s-cwq7g7` tracks seeing the window on a Windows whose WSL keeps AVX.
- The VM has no GPU, so WSLg and the app draw in software.
- The evaluation activates online during setup and then runs 90 days. Until it
  activates, it reports an expired license and shuts Windows down an hour after
  each boot. After 90 days, delete the VM and create it again.
- One VM per machine. It lives in `~/.local/share/attn-windows-vm`, or
  `$ATTN_WINDOWS_VM_DIR`, with the ISO, the SSH key and the Windows password.
  Windows SSH listens on `127.0.0.1:2522` (`ATTN_WINDOWS_VM_SSH_PORT`).
- The VM has 12 CPUs and 24 GiB of memory (`ATTN_WINDOWS_VM_CPUS`,
  `ATTN_WINDOWS_VM_MEMORY_GIB`). WSL gets two thirds of the memory.
- `install REV` checks the commit out in a worktree at
  `~/.local/share/attn-windows-vm/source`.
