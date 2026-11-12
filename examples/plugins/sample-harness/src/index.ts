import { randomUUID } from "node:crypto";
import { createConnection } from "node:net";
import { rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  AttnPluginClient,
  deferred,
  type DriverDeliverResult,
  type DriverSpawnParams,
  type DriverRun,
} from "@victorarias/attn-plugin";

import { conversationExists, inputSocketPath, transcriptPath } from "./state";

const harnessId = "sample";
const harnessScript = join(dirname(fileURLToPath(import.meta.url)), "harness.ts");

const client = new AttnPluginClient({ version: "0.1.0" });

client.driver({
  id: harnessId,
  initialPrompt: true,
  attributedInput: true,
  safeDuringQuestion: true,
  stateReporting: true,
  spawn: (params, run) => launch(params, run, randomUUID()),
  resume: (params, run) => {
    const conversationId = params.resume_session_id || storedConversation(params);
    if (!conversationExists(conversationId)) throw new Error(`conversation ${conversationId} is not stored`);
    return launch(params, run, conversationId);
  },
  resumeAvailable: (conversations) =>
    conversations.map((conversation) => ({
      ...conversation,
      available: conversationExists(conversation.resume_session_id),
      reason: `conversation ${conversation.resume_session_id} is not stored`,
    })),
  deliver: async (input, run) => {
    await run.state("working");
    const result = await sendToHarness(inputSocketPath(input.run_id), input);
    await run.state("idle");
    return result;
  },
  sessionClosed: (closed) => rmSync(inputSocketPath(closed.run_id), { force: true }),
});

await client.connect();

async function launch(params: DriverSpawnParams, run: DriverRun, conversationId: string) {
  await run.reportMetadata({ conversationId }, { resumeSessionId: conversationId });
  await run.state("idle");
  const argv = [
    process.execPath,
    harnessScript,
    "--conversation",
    conversationId,
    "--transcript",
    transcriptPath(conversationId),
    "--socket",
    inputSocketPath(params.run_id),
  ];
  if (params.initial_prompt) argv.push("--prompt", params.initial_prompt);
  return { argv, cwd: params.cwd };
}

function storedConversation(params: DriverSpawnParams): string {
  const metadata = params.metadata as { conversationId?: string } | undefined;
  if (!metadata?.conversationId) throw new Error("resume needs a stored conversation ID");
  return metadata.conversationId;
}

function sendToHarness(
  socketPath: string,
  input: { input_id: string; text: string; voice?: string; conversation_id?: string },
): Promise<DriverDeliverResult> {
  return new Promise((resolve, reject) => {
    const socket = createConnection({ path: socketPath });
    let reply = "";
    socket.setEncoding("utf8");
    socket.once("error", () => resolve(deferred("the harness is not listening")));
    socket.on("data", (chunk) => (reply += chunk));
    socket.once("end", () => {
      try {
        resolve(reply.trim() === "" ? deferred() : (JSON.parse(reply) as DriverDeliverResult));
      } catch (error) {
        reject(error);
      }
    });
    socket.write(
      `${JSON.stringify({ input_id: input.input_id, text: input.text, voice: input.voice, conversation_id: input.conversation_id })}\n`,
    );
  });
}
