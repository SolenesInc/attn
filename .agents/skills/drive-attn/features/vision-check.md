# Vision check

Ask a model one question about an image. An agent that took a screenshot
asks, for example, whether a button is blue, and gets one answer back on
stdout, with the model, cost and turns on stderr. It is one call to Claude
Code, whatever harness the agent runs in, and it never touches the daemon.

## Sub-features

- `vision-ask`: one image (`.png`, `.jpg`, `.jpeg`, `.gif` or `.webp`) and one question, one answer.
- `vision-json`: `--json` prints one line with the answer, model, cost, turns and `is_error`.
- `vision-model`: `--model` picks the model (`sonnet` by default) and `--timeout` bounds the call (120 seconds by default).

## How to get to it

### People

- Their own shell: the same verb agents use, below. Nothing in the app reaches it.

### Agents

- `attn vision-check <image> <question> [--model sonnet] [--timeout 120s] [--json]` (`cmd/attn/vision_check.go`).
- Prompts: none. Agents find the verb in `attn --help`.

## Driving it

Preconditions:

- The baseline instance. Vision check needs no daemon and no session, so `./attn` in the instance shell stands in for the agent.
- A proof folder holding a stand-in `claude` and an image, so no real model is called:

```bash
P=$(mktemp -d /tmp/drive-attn-vision-check.XXXX) && echo "export P=$P" >> $DRIVE_ENV && mkdir -p $P/bin
cat > $P/bin/claude <<EOF
#!/bin/sh
printf '%s\n' "\$@" > $P/argv
cat > $P/stdin
echo '{"type":"result","subtype":"success","is_error":false,"result":"the button is blue","total_cost_usd":0.0234,"num_turns":1}'
EOF
chmod +x $P/bin/claude
printf 'PNG pixels' > $P/shot.png
```

- **Ask.** Run `PATH="$P/bin:$PATH" ./attn vision-check $P/shot.png "What color is the button?"`. It prints `the button is blue` on stdout and `vision-check: model=sonnet cost_usd=0.0234 turns=1` on stderr, and exits 0. `$P/argv` holds `-p`, `--model`, `sonnet`, `--max-turns` and `1`; `$P/stdin` holds one JSON user message with the question and the image as base64 `image/png`.
- **JSON.** Add `--json` to the same command. It prints `{"answer":"the button is blue","model":"sonnet","cost_usd":0.0234,"num_turns":1,"is_error":false}` on stdout and nothing on stderr.
- **Another model.** Add `--model opus --timeout 30s`. `$P/argv` shows `opus` after `--model`.
- **Refusals.** `./attn vision-check $P/shot.bmp "x"` exits 1 with `vision-check: unsupported image extension ".bmp" (supported: .png, .jpg, .jpeg, .gif, .webp)`. `./attn vision-check $P/shot.png` exits 2 with `vision-check: usage: attn vision-check <image> <question> [--model sonnet] [--timeout 120s] [--json]`. A model error, the stand-in edited to answer `"is_error":true`, prints its `result` text on stderr and exits 1.
- **Proof.** In `$P`, keep each command with its stdout, stderr and exit code, and `argv` and `stdin` after the first ask. Put the way in in each file's name, such as `vision-check-shell-json.txt`.

## Contexts

- **Linux**: works the same where the CLI runs on Linux. It finds `claude` on `PATH`, in `~/.npm-global/bin` or in `~/.local/bin`.
- **Outposts**: it runs on the machine where it is called and reads the image from that machine's disk; neither the home nor the outpost's daemon takes part.
- **Performance and idle cost**: one model call of one turn. The image may be at most 4,500,000 bytes once base64-encoded, about 3.37 million bytes on disk.
- **Harnesses**: it always calls Claude Code, whatever harness the agent runs in, so a Codex or Pi agent needs Claude Code installed and signed in. The call runs one turn with no allowed tools, `--strict-mcp-config` and empty `--setting-sources`.

## Tests

- Stack: `cmd/attn/vision_check_stack_test.go`.

## Gotchas

- It runs the `claude` it finds, not the instance's mock agent: neither `ATTN_CLAUDE_EXECUTABLE` nor the baseline's refusal of headless model tasks reaches it. Without the stand-in it sends the image to a real model and costs money.
- `./attn vision-check --help` prints `vision-check: flag: help requested` and exits 2. Run it with no arguments to see the usage line.
- The extension decides the image type, not the bytes: a JPEG named `.png` is sent as `image/png`.
- Working but wrong: over the size limit it says to "capture with --crop/--max-dim first". Those are flags of the maintainers' `app/scripts/real-app-harness/capture-app-screenshot.mjs`; the agents attn runs have no such tool.
