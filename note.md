Ask commands open in a terminal next to the ask. Each `sh`/`bash`/`zsh`/`fish`/`shell` block in an ask gets **Open in terminal** (`t` for the first one). It opens a shell that the ask holds, in the Asks panel, on the machine and in the folder the block names (```sh studio:/srv/shop`), with the command typed but not run. The shell has the keyboard; the Asks chord gives it back. Send reads **Send and stop** while a command runs, and answering ends the ask's shells. **Move to desktop** puts a shell in a tile next to the asking agent, and it stays after the answer. Only the shown ask's shells stream to the app. The phone shows Copy and `machine · folder` under each command (new optional `commands`/`runs_at` on the gateway ask).

Checked live: on Linux under Xvfb with an outpost ask on attn-linux, and on macOS in the VM.

![Linux: a command running in the panel, Send and stop](https://land-feed.caketroll.cloud/m/SolenesInc/attn/9bd73198a9bf6c5baabdeec71afa504d05aa56af1a0f902f1c03cfc767653b69.png)
![macOS VM: Send and stop](https://land-feed.caketroll.cloud/m/SolenesInc/attn/d28501efa865f0803741f8a8a4924cbdbc7d38ee48a46ccca51b9124bc15f236.png)
![macOS VM run](https://land-feed.caketroll.cloud/m/SolenesInc/attn/ed7e1c234ba698e2fe2a11166d73cddce5b61b4f7fe7b2c55204ecc26338723f.mp4)

Known limits:
- Multi-line blocks are typed as one joined line, because the typed-shell paste strips control characters (plot s-20581r D7). The Orchestrator has this.
- Only top-level blocks get the button on the desktop.
