import { appendFileSync, existsSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { createInterface } from "node:readline";

type Input = { input_id: string; text: string; voice: "user" | "attn"; conversation_id?: string };

const args = process.argv.slice(2);
const conversationId = flag("--conversation");
const transcript = flag("--transcript");
const socketPath = flag("--socket");
const firstPrompt = args.includes("--prompt") ? flag("--prompt") : "";

const resumed = existsSync(transcript);
appendFileSync(transcript, "");

const received = new Map<string, true>();

function accept(who: string, text: string): void {
  appendFileSync(transcript, `${who}: ${text}\n`);
  console.log(`[${who}] ${text}`);
  console.log(`echo: ${text}`);
}

rmSync(socketPath, { force: true });
createServer((socket) => {
  socket.setEncoding("utf8");
  let buffer = "";
  socket.on("data", (chunk) => {
    buffer += chunk;
    const newline = buffer.indexOf("\n");
    if (newline === -1) return;
    const input = JSON.parse(buffer.slice(0, newline)) as Input;
    buffer = buffer.slice(newline + 1);
    if (input.conversation_id && input.conversation_id !== conversationId) {
      socket.end(`${JSON.stringify({ ok: false, status: "target_ended", reason: "different conversation" })}\n`);
      return;
    }
    if (!received.has(input.input_id)) {
      received.set(input.input_id, true);
      accept(input.voice, input.text);
    }
    socket.end(`${JSON.stringify({ ok: true })}\n`);
  });
}).listen(socketPath, () => {
  console.log(`sample harness ${resumed ? "resumed" : "started"} conversation ${conversationId}`);
  if (firstPrompt) accept("you", firstPrompt);
});

createInterface({ input: process.stdin }).on("line", (line) => accept("you", line));

function flag(name: string): string {
  const value = args[args.indexOf(name) + 1];
  if (!value) throw new Error(`${name} is required`);
  return value;
}
