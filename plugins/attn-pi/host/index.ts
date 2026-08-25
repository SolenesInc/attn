// Envelopes go out on fd 3, not stdout: pi loads the user's own extensions, and any one of them printing would corrupt a shared stream.

import { createWriteStream } from "node:fs";
import { existsSync, mkdirSync, readdirSync } from "node:fs";
import { open } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import {
  createAgentSession,
  DefaultResourceLoader,
  SessionManager,
  SettingsManager,
  VERSION as PI_VERSION,
} from "@earendil-works/pi-coding-agent";
// getModel is exported only from the /compat subpath at this pin, where it is deprecated but working.
import { getModel } from "@earendil-works/pi-ai/compat";
// Inlined at build time: pi reads its own VERSION off disk at runtime, which a `bun build --compile` binary has no copy of, so it degrades to "0.0.0".
import piPlugin from "../package.json" with { type: "json" };

import {
  DeltaCoalescer,
  EnvelopeStream,
  PiEventMapper,
  SNAPSHOT_BYTES_LIMIT,
  SNAPSHOT_ITEM_LIMIT,
  TRANSCRIPT_RETENTION_BYTES,
  TRANSCRIPT_RETENTION_ITEMS,
  ToolDetailStore,
  TranscriptStore,
  conversationInterrupted,
  launchPromptIsUndelivered,
  parseVerb,
  reconstructTranscript,
  retentionBudget,
  type Envelope,
  type HostSessionState,
  type HostVerb,
  type HostVerbWithText,
  type ModelChangedBody,
  type RunSettledBody,
  type SessionEntryLike,
  type SessionReadyBody,
  type ToolDetailBody,
} from "./envelope";

/** Receipt on DeltaCoalescer (envelope.ts): ~1,970 events/s bursts against a 256-message client buffer. */
const DELTA_WINDOW_MS = 30;

/** Receipt (2026-08-06, compiled host, pi 0.83.0): an 8-call exploration run retained 52,463 bytes and a truncated one 10,342; 16 MB is ~320x the heavier, against a 130 MB host floor. */
const TOOL_DETAIL_BUDGET_BYTES = 16 << 20;

/** 80x pi's own 50 KB in-result cap (~50,000 terminal lines) and an order of magnitude under the daemon's 64 MB envelope teardown; a longer file is answered with its last 4 MB. */
const FULL_OUTPUT_LIMIT_BYTES = 4 << 20;

const ENVELOPE_FD = 3;

const PINNED_PI_VERSION = piPlugin.dependencies["@earendil-works/pi-coding-agent"];

/** pi has no extension/version compat gate: a mismatched build loads silently and fails at the first missing call site. */
function requirePinnedPi(): string {
  if (PI_VERSION !== "0.0.0" && PI_VERSION !== PINNED_PI_VERSION) {
    throw new Error(
      `loaded pi ${PI_VERSION} is not the pinned ${PINNED_PI_VERSION}; reinstall plugins/attn-pi dependencies`,
    );
  }
  return PINNED_PI_VERSION;
}

function requireEnv(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) {
    throw new Error(`${name} is required; attn's daemon sets it when it spawns nisse`);
  }
  return value;
}

function optionalEnv(name: string): string {
  return process.env[name]?.trim() ?? "";
}

function retentionFromEnv(name: string, fallback: number): number {
  return retentionBudget(name, optionalEnv(name), fallback, (message) =>
    console.error(`[nisse] ${message}`));
}

// A fork, not an open-in-place: two hosts writing one session dir would corrupt the
// history the user resumed from. Only an EMPTY dir consults the resume file.
function openSession(
  cwd: string,
  sessionDir: string,
  resumeFile: string,
): { sessionManager: SessionManager; forked: boolean } {
  if (resumeFile !== "" && !holdsSession(sessionDir)) {
    console.error(`[nisse] forking ${resumeFile} into ${sessionDir}`);
    return { sessionManager: SessionManager.forkFrom(resumeFile, cwd, sessionDir), forked: true };
  }
  return { sessionManager: SessionManager.continueRecent(cwd, sessionDir), forked: false };
}

function holdsSession(sessionDir: string): boolean {
  try {
    return readdirSync(sessionDir).some((name) => name.endsWith(".jsonl"));
  } catch {
    return false;
  }
}

function resolveModel(pinned: string) {
  const split = pinned.indexOf("/");
  if (split <= 0 || split === pinned.length - 1) {
    throw new Error(`model ${JSON.stringify(pinned)} must be "provider/model-id" (e.g. "openai/gpt-5.6-luna")`);
  }
  const provider = pinned.slice(0, split);
  const id = pinned.slice(split + 1);
  try {
    return getModel(provider, id);
  } catch (error) {
    throw new Error(`pi has no model ${JSON.stringify(pinned)}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function readOutputTail(path: string, limit: number): Promise<{ text: string; clipped: boolean; size: number }> {
  const handle = await open(path, "r");
  try {
    const { size } = await handle.stat();
    if (size <= limit) {
      return { text: await handle.readFile("utf8"), clipped: false, size };
    }
    const buffer = Buffer.alloc(limit);
    await handle.read(buffer, 0, limit, size - limit);
    return { text: buffer.toString("utf8"), clipped: true, size };
  } finally {
    await handle.close();
  }
}

async function main(): Promise<void> {
  const piVersion = requirePinnedPi();
  const sessionID = requireEnv("ATTN_NISSE_SESSION_ID");
  const sessionDir = requireEnv("ATTN_NISSE_SESSION_DIR");
  const cwd = requireEnv("ATTN_NISSE_CWD");
  const pinnedModel = requireEnv("ATTN_NISSE_MODEL");
  const initialPrompt = process.env.ATTN_NISSE_INITIAL_PROMPT?.trim() ?? "";
  const resumeFile = optionalEnv("ATTN_NISSE_RESUME_FILE");

  const envelopeOut = createWriteStream("", { fd: ENVELOPE_FD });
  const epoch = randomUUID();
  const transcript = new TranscriptStore(
    epoch,
    SNAPSHOT_ITEM_LIMIT,
    SNAPSHOT_BYTES_LIMIT,
    retentionFromEnv("ATTN_NISSE_RETENTION_ITEMS", TRANSCRIPT_RETENTION_ITEMS),
    retentionFromEnv("ATTN_NISSE_RETENTION_BYTES", TRANSCRIPT_RETENTION_BYTES),
  );
  const write = (envelope: Envelope) => {
    transcript.apply(envelope.kind, envelope.body);
    envelopeOut.write(`${JSON.stringify(envelope)}\n`);
  };

  const model = resolveModel(pinnedModel);
  if (!existsSync(sessionDir)) mkdirSync(sessionDir, { recursive: true });

  // Session storage is attn's; auth and resource discovery still resolve against the real ~/.pi/agent dir.
  const agentDir = join(homedir(), ".pi", "agent");
  // A host killed before pi's first assistant message leaves no session file at all
  // (measured, 2026-08-04), so the relaunch is an ordinary fresh start.
  const { sessionManager, forked } = openSession(cwd, sessionDir, resumeFile);
  const settingsManager = SettingsManager.create(cwd);
  const resourceLoader = new DefaultResourceLoader({ cwd, agentDir, settingsManager });
  await resourceLoader.reload();

  const { session } = await createAgentSession({ cwd, model, sessionManager, settingsManager, resourceLoader });

  // Required: without it `session_start` never fires and extensions silently do nothing (receipt: 2026-08-04 spike).
  await session.bindExtensions({ mode: "print" });

  const stream = new EnvelopeStream(sessionID, write);
  const deltas = new DeltaCoalescer(DELTA_WINDOW_MS, (id, text) => stream.emit("message_delta", { id, text }));
  const toolDetails = new ToolDetailStore(TOOL_DETAIL_BUDGET_BYTES);
  const mapper = new PiEventMapper(stream, deltas, (type) => {
    console.error(`[nisse] unmapped pi event type ${type} (pi ${piVersion})`);
  }, toolDetails);

  session.subscribe((event) => {
    mapper.handle(event as { type: string });
    if ((event as { type?: string }).type === "agent_settled") {
      console.error(
        `[nisse] holding ${toolDetails.retainedBytes} bytes of tool detail ` +
        `across ${toolDetails.size} call(s), budget ${TOOL_DETAIL_BUDGET_BYTES} bytes`,
      );
      console.error(
        `[nisse] transcript holding ${transcript.retainedBytes} bytes across ${transcript.size} item(s), ` +
        `${transcript.droppedItems} dropped past retention`,
      );
    }
  });

  // `buildContextEntries` is the compaction-aware path pi itself sends to the model.
  const history = reconstructTranscript(sessionManager.buildContextEntries() as SessionEntryLike[]);
  transcript.seed(history.items);
  for (const [callID, detail] of history.details) toolDetails.put(callID, detail);
  const interrupted = conversationInterrupted(history.items);
  if (history.items.length > 0) {
    console.error(
      `[nisse] revived ${history.items.length} item(s) and ${history.details.size} tool detail(s) ` +
      `from ${session.sessionFile ?? "(no file)"}; interrupted=${interrupted}`,
    );
  }

  const currentModelName = (): string => {
    const model = session.model;
    return model ? `${model.provider}/${model.id}` : "";
  };

  let availableModels: string[] = [];
  try {
    availableModels = (await session.modelRuntime.getAvailable()).map((entry) => `${entry.provider}/${entry.id}`);
  } catch (error) {
    console.error(`[nisse] listing available models failed: ${error instanceof Error ? error.message : String(error)}`);
  }

  const readyState: HostSessionState = interrupted ? "waiting_input" : "idle";
  const ready: SessionReadyBody = {
    session_file: session.sessionFile ?? null,
    model: currentModelName() || pinnedModel,
    cwd,
    pi_version: piVersion,
    state: readyState,
    models: availableModels,
  };
  stream.emit("session_ready", ready);
  // `session_ready` resets a client's spine and this refills it, in that order.
  stream.emit("conversation_snapshot", transcript.snapshot());

  let running = false;
  let shuttingDown = false;

  const runPrompt = async (text: string, inputID?: string) => {
    running = true;
    try {
      await session.prompt(text);
    } catch (error) {
      if (inputID) mapper.forgetInput(inputID);
      // A prompt can fail before pi opens a run, in which case no agent_settled ever arrives
      // and the composer would stay closed forever; settle the run here.
      const message = error instanceof Error ? error.message : String(error);
      console.error(`[nisse] prompt failed: ${error instanceof Error ? error.stack : String(error)}`);
      deltas.flush();
      const settled: RunSettledBody = { state: "idle", error: message };
      stream.emit("run_settled", settled);
    } finally {
      running = false;
    }
  };

  // Receipt (2026-08-04 spike, re-validated at pi 0.83.0): a steer drains at the next turn
  // boundary, a follow-up only when the run would otherwise settle.
  const deliver = async (verb: HostVerbWithText) => {
    if (verb.inputID) mapper.expectInput(verb.inputID, verb.text);
    if (!running) {
      if (verb.verb === "prompt") return runPrompt(verb.text, verb.inputID);
      console.error(`[nisse] ${verb.verb} on an idle session: starting a run`);
      return runPrompt(verb.text, verb.inputID);
    }
    if (verb.verb === "prompt") {
      console.error("[nisse] refused prompt: a run is already open");
      if (verb.inputID) mapper.forgetInput(verb.inputID);
      return;
    }
    try {
      if (verb.verb === "steer") await session.steer(verb.text);
      else await session.followUp(verb.text);
    } catch (error) {
      if (verb.inputID) mapper.forgetInput(verb.inputID);
      console.error(`[nisse] ${verb.verb} failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  const sendToolDetail = async (callID: string, full: boolean) => {
    const held = toolDetails.get(callID);
    if (!held) {
      const reason = toolDetails.missingReason(callID);
      console.error(`[nisse] ${reason}`);
      const body: ToolDetailBody = { call_id: callID, text: "", full: false, truncated: false, error: reason };
      stream.emit("tool_detail", body);
      return;
    }
    const body: ToolDetailBody = {
      call_id: callID,
      text: held.text,
      full: false,
      truncated: held.truncated,
      ...(held.patch === undefined ? {} : { patch: held.patch }),
      ...(held.fullOutputPath === undefined ? {} : { full_output_path: held.fullOutputPath }),
    };
    if (full && held.fullOutputPath) {
      try {
        const read = await readOutputTail(held.fullOutputPath, FULL_OUTPUT_LIMIT_BYTES);
        body.text = read.text;
        body.full = true;
        body.truncated = read.clipped;
        if (read.clipped) {
          body.error =
            `showing the last ${FULL_OUTPUT_LIMIT_BYTES >> 20} MB of ${read.size} bytes; ` +
            `the whole output is at ${held.fullOutputPath}`;
        }
      } catch (error) {
        body.error = `could not read ${held.fullOutputPath}: ${error instanceof Error ? error.message : String(error)}`;
      }
    }
    stream.emit("tool_detail", body);
  };

  const setModel = async (pinned: string) => {
    const body: ModelChangedBody = { model: pinned };
    try {
      await session.setModel(resolveModel(pinned));
      body.model = currentModelName() || pinned;
      console.error(`[nisse] model switched to ${body.model}`);
    } catch (error) {
      body.model = currentModelName() || pinnedModel;
      body.error = error instanceof Error ? error.message : String(error);
      console.error(`[nisse] set_model ${pinned} refused: ${body.error}`);
    }
    stream.emit("model_changed", body);
  };

  const shutdown = async () => {
    if (shuttingDown) return;
    shuttingDown = true;
    // AgentSession.dispose() emits nothing (pi 0.83.0), so emit the goodbye here in the
    // runtime's order: handlers first, dispose after.
    try {
      await session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" });
    } catch (error) {
      console.error(`[nisse] session_shutdown failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    // Cooperative teardown is the only kind that reaches pi's tool subprocesses: a hard kill
    // orphans them (receipt: 3x reproduced, 2026-08-04 spike).
    try {
      session.dispose();
    } catch (error) {
      console.error(`[nisse] dispose failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    envelopeOut.end();
    process.exit(0);
  };

  process.on("SIGTERM", () => void shutdown());
  process.on("SIGINT", () => void shutdown());

  const handleVerb = (verb: HostVerb) => {
    switch (verb.verb) {
      case "prompt":
      case "steer":
      case "follow_up":
        void deliver(verb);
        return;
      case "tool_detail":
        void sendToolDetail(verb.callID, verb.full);
        return;
      case "snapshot": {
        stream.emit("conversation_snapshot", transcript.snapshot());
        return;
      }
      case "history": {
        stream.emit("conversation_page", transcript.page(verb.before));
        return;
      }
      case "set_model":
        void setModel(verb.model);
        return;
      case "clear_queue": {
        const dropped = session.clearQueue();
        console.error(
          `[nisse] cleared the queue: ${dropped.steering.length} steering, ${dropped.followUp.length} follow-up`,
        );
        return;
      }
      case "shutdown":
        void shutdown();
        return;
    }
  };

  if (initialPrompt !== "") {
    if (launchPromptIsUndelivered(initialPrompt, history.items, forked)) {
      console.error(
        `[nisse] delivering the launch prompt (${initialPrompt.length} chars) into a conversation ` +
          `that has not been told what it is for (forked=${forked}, ${history.items.length} item(s) reopened)`,
      );
      void runPrompt(initialPrompt);
    } else {
      console.error(`[nisse] launch prompt already delivered; ${history.items.length} item(s) reopened`);
    }
  }

  let buffer = "";
  for await (const chunk of process.stdin) {
    buffer += Buffer.from(chunk as Uint8Array).toString("utf8");
    let newline = buffer.indexOf("\n");
    while (newline >= 0) {
      const line = buffer.slice(0, newline).trim();
      buffer = buffer.slice(newline + 1);
      if (line !== "") {
        try {
          handleVerb(parseVerb(line));
        } catch (error) {
          console.error(`[nisse] bad verb: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
      newline = buffer.indexOf("\n");
    }
  }

  await shutdown();
}

main().catch((error) => {
  console.error(`[nisse] fatal: ${error instanceof Error ? error.stack : String(error)}`);
  process.exit(1);
});
