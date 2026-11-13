import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer, type Server, type Socket } from "node:net";

import {
  AttnPluginClient,
  accepted,
  deferred,
  rejected,
  targetEnded,
  type DriverDefinition,
  type DriverRun,
} from "../src";

type Message = {
  jsonrpc: "2.0";
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: unknown;
  error?: { code: number; message: string };
};

type FakeDaemon = {
  socketPath: string;
  requests: Message[];
  call(method: string, params: Record<string, unknown>): Promise<Message>;
  next(method: string): Promise<Message>;
  response(id: number): Promise<Message>;
};

const cleanups: (() => Promise<void>)[] = [];

afterEach(async () => {
  while (cleanups.length > 0) {
    await cleanups.pop()?.();
  }
});

const baseDriver = { id: "sample", spawn: () => ({ argv: ["sample"] }) } satisfies DriverDefinition;

describe("AttnPluginClient driver", () => {
  test("registers capabilities derived from handlers and declared promises", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    client.driver({
      ...baseDriver,
      displayName: "Example Agent",
      resume: () => ({ argv: ["sample"] }),
      resumeAvailable: () => [],
      models: () => ({ models: [] }),
      deliver: () => accepted(),
      compact: () => accepted(),
      attributedInput: true,
      safeDuringQuestion: true,
      initialPrompt: true,
    });
    await client.connect();

    const register = daemon.requests.find((request) => request.method === "driver.register");
    expect(register?.params).toEqual({
      agent: "sample",
      display_name: "Example Agent",
      capabilities: {
        resume: true,
        resume_availability: true,
        model_discovery: true,
        message_delivery: true,
        compaction: true,
        attributed_input: true,
        safe_during_question: true,
        initial_prompt: true,
      },
    });
  });

  test("registers a shared server binding without terminal delivery handlers", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    const definition: DriverDefinition = {
      ...baseDriver,
      server: {
        protocol: "attn-app-server-v1", executable: "sample", serveArgs: ["serve"],
        homeEnv: "SAMPLE_HOME", defaultHome: ".sample", socketName: "sample.sock",
        initialize: { server: "sample", protocol: { exact: 1 } },
        callerConversationEnv: "SAMPLE_ROOT_CONVERSATION", onClose: "leave-running",
        recovery: "numbered-completions", targets: true,
      },
    };
    client.driver(definition);
    await client.connect();
    const registered = daemon.requests.find(request => request.method === "driver.register");
    expect(registered?.params).toEqual({
      agent: "sample", capabilities: { server_binding: true },
      server: {
        protocol: "attn-app-server-v1", executable: "sample", serve_args: ["serve"],
        home_env: "SAMPLE_HOME", default_home: ".sample", socket_name: "sample.sock",
        initialize: { server: "sample", protocol: { exact: 1 } },
        caller_conversation_env: "SAMPLE_ROOT_CONVERSATION", on_close: "leave-running",
        recovery: "numbered-completions", targets: true,
      },
    });
    const legacyDaemon = await startDaemon({ "driver.register": new Error('unsupported driver capability "server_binding"') });
    const legacyClient = connectable(legacyDaemon);
    legacyClient.driver(definition);
    await expect(legacyClient.connect()).rejects.toThrow(/does not support server_binding.*attn-app-server-v1/);
    expect(() => connectable(daemon).driver({ ...baseDriver, server: {
      protocol: "attn-app-server-v1", executable: "sample", serveArgs: ["serve"],
      homeEnv: "SAMPLE_HOME", defaultHome: ".sample", socketName: "sample.sock",
      initialize: { protocol: { min: 7 } }, callerConversationEnv: "SAMPLE_ROOT_CONVERSATION",
      onClose: "interrupt", recovery: "last-run",
    }, deliver: () => accepted() })).toThrow("server binding supplies");
  });

  test("keeps the driver out of the hello surfaces", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    client.handle<"worktree.after_create">("worktree.after_create", () => {});
    client.driver(baseDriver);
    await client.connect();

    expect(daemon.requests.map((request) => request.method)).toEqual(["hello", "driver.register"]);
    expect(daemon.requests[0]?.params?.surfaces).toEqual(["worktree.after_create"]);
    expect(daemon.requests[1]?.params).toEqual({ agent: "sample", capabilities: {} });
  });

  test("answers a call for an omitted capability as an unknown method", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    client.driver(baseDriver);
    await client.connect();

    for (const method of ["driver.deliver_message", "driver.compact", "driver.resume", "driver.models", "driver.resume_available"]) {
      const answer = await daemon.call(method, { session_id: "t", run_id: "r", conversations: [] });
      expect(answer.error?.message).toBe(`unknown method ${method}`);
    }
  });

  test("hands a spawn the launch and returns the command", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    const seen: { cwd: string; terminal: string; run: string }[] = [];
    client.driver({
      ...baseDriver,
      spawn: (params, run) => {
        seen.push({ cwd: params.cwd, terminal: run.terminalId, run: run.runId });
        return { argv: ["sample", "--model", params.model ?? ""], env: { A: "1" }, cwd: params.cwd };
      },
      modelPin: true,
    });
    await client.connect();

    const answer = await daemon.call("driver.spawn", { agent: "sample", session_id: "t1", run_id: "r1", cwd: "/work", model: "m" });
    expect(answer.result).toEqual({ argv: ["sample", "--model", "m"], env: { A: "1" }, cwd: "/work" });
    expect(seen).toEqual([{ cwd: "/work", terminal: "t1", run: "r1" }]);
  });

  test("passes attribution to deliver and returns each outcome unchanged", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    const outcomes = [accepted(), deferred(), rejected("no"), targetEnded()];
    const inputs: unknown[] = [];
    client.driver({
      ...baseDriver,
      attributedInput: true,
      deliver: (params) => {
        inputs.push([params.voice, params.conversation_id, params.text]);
        return outcomes.shift()!;
      },
    });
    await client.connect();

    const answers = [];
    for (const id of ["a", "b", "c", "d"]) {
      answers.push(
        (await daemon.call("driver.deliver_message", { session_id: "t", run_id: "r", input_id: id, text: id, voice: "attn", conversation_id: "native" })).result,
      );
    }
    expect(answers).toEqual([
      { ok: true },
      { ok: false, status: "deferred", reason: "Recipient unavailable" },
      { ok: false, status: "rejected", reason: "no" },
      { ok: false, status: "target_ended", reason: "Target ended" },
    ]);
    expect(inputs[0]).toEqual(["attn", "native", "a"]);
  });

  test("continues the report sequence of a run attn still holds after a reconnect", async () => {
    const daemon = await startDaemon({
      hello: { ok: true, active_runs: [{ session_id: "t1", run_id: "r1", metadata: { c: "native" }, seq: 7 }] },
      "driver.register": { ok: true, active_runs: [{ session_id: "t1", run_id: "r1", metadata: { c: "native" }, seq: 7 }] },
    });
    const client = connectable(daemon);
    let recovered: DriverRun[] = [];
    client.driver({ ...baseDriver, recover: (runs) => void (recovered = runs) });
    await client.connect();

    expect(recovered.map((run) => [run.terminalId, run.runId, run.metadata])).toEqual([["t1", "r1", { c: "native" }]]);
    await recovered[0]!.state("working");
    await recovered[0]!.stop("idle");
    await recovered[0]!.reportMetadata({ c: "next" }, { resumeSessionId: "next" });

    const reports = daemon.requests.filter((request) => request.method?.startsWith("session."));
    expect(reports.map((report) => [report.method, report.params?.seq])).toEqual([
      ["session.report_state", 8],
      ["session.report_stop", 9],
      ["session.report_metadata", 10],
    ]);
    expect(reports[2]?.params).toMatchObject({ session_id: "t1", run_id: "r1", resume_session_id: "next" });
  });

  test("recovers a run before accepting input sent as the driver registers", async () => {
    const active = [{ session_id: "t1", run_id: "r1", metadata: { c: "native" }, seq: 7 }];
    const daemon = await startDaemon(
      { hello: { ok: true, active_runs: active }, "driver.register": { ok: true, active_runs: active } },
      (message, socket) => {
        if (message.method === "driver.register") {
          socket.write(`${JSON.stringify({ jsonrpc: "2.0", id: 900, method: "driver.deliver_message", params: {
            session_id: "t1", run_id: "r1", input_id: "i1", text: "hello",
          } })}\n`);
        }
      },
    );
    const client = connectable(daemon);
    let ready = false;
    let delivered: { ready: boolean; metadata: unknown } | undefined;
    client.driver({
      ...baseDriver,
      recover: async () => { await Promise.resolve(); ready = true; },
      deliver: async (_input, run) => {
        delivered = { ready, metadata: run.metadata };
        await run.state("working");
        return accepted();
      },
    });
    await client.connect();
    const report = await daemon.next("session.report_state");
    await daemon.response(900);
    expect(delivered).toEqual({ ready: true, metadata: { c: "native" } });
    expect(report.params?.seq).toBe(8);
  });

  test("defers input on an older daemon until recovery and registers again", async () => {
    const active = [{ session_id: "t1", run_id: "r1", metadata: { c: "native" }, seq: 7 }];
    let registrations = 0;
    const daemon = await startDaemon(
      { hello: { ok: true }, "driver.register": { ok: true, active_runs: active } },
      (message, socket) => {
        if (message.method !== "driver.register") return;
        registrations++;
        socket.write(`${JSON.stringify({ jsonrpc: "2.0", id: 900 + registrations, method: "driver.deliver_message", params: {
          session_id: "t1", run_id: "r1", input_id: "i1", text: "hello",
        } })}\n`);
      },
    );
    const client = connectable(daemon);
    let release!: () => void;
    const recovery = new Promise<void>((resolve) => { release = resolve; });
    let ready = false;
    let delivered: unknown;
    client.driver({
      ...baseDriver,
      recover: async () => { await recovery; ready = true; },
      deliver: async (_input, run) => {
        delivered = { ready, metadata: run.metadata };
        await run.state("working");
        return accepted();
      },
    });
    const connected = client.connect();
    expect((await daemon.response(901)).result).toMatchObject({ ok: false, status: "deferred" });
    release();
    await connected;
    expect((await daemon.response(902)).result).toEqual({ ok: true });
    expect(delivered).toEqual({ ready: true, metadata: { c: "native" } });
    expect((await daemon.next("session.report_state")).params?.seq).toBe(8);
    expect(registrations).toBe(2);
  });

  test("delivers a close notification after asynchronous recovery", async () => {
    const daemon = await startDaemon({ hello: { ok: true, active_runs: [{ session_id: "t1", run_id: "r1", seq: 7 }] } });
    const client = connectable(daemon);
    let release!: () => void;
    let started!: () => void;
    const recovery = new Promise<void>((resolve) => { release = resolve; });
    const recovering = new Promise<void>((resolve) => { started = resolve; });
    let closed = false;
    client.driver({
      ...baseDriver,
      recover: async () => { started(); await recovery; },
      sessionClosed: () => { closed = true; },
    });
    const connected = client.connect();
    await recovering;
    const close = daemon.call("driver.session_closed", { session_id: "t1", run_id: "r1", reason: "killed" });
    await daemon.call("attn.health", {});
    expect(closed).toBe(false);
    release();
    expect((await close).result).toEqual({ ok: true });
    await connected;
    expect(closed).toBe(true);
  });

  test("reports compaction by input and conversation", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    let outcomeReported: Promise<void> = Promise.resolve();
    client.driver({
      ...baseDriver,
      deliver: () => accepted(),
      compact: (params, run) => {
        outcomeReported = run.compaction({ inputId: params.input_id, conversationId: params.conversation_id, outcome: "completed" });
        return accepted();
      },
    });
    await client.connect();

    const reported = daemon.next("session.report_compaction");
    const answer = await daemon.call("driver.compact", { session_id: "t", run_id: "r", input_id: "i", conversation_id: "native", text: "" });
    expect(answer.result).toEqual({ ok: true });
    await outcomeReported;
    expect((await reported).params).toEqual({ session_id: "t", run_id: "r", input_id: "i", conversation_id: "native", outcome: "completed" });
  });

  test("explains a daemon that refuses a capability", async () => {
    const daemon = await startDaemon({ "driver.register": new Error('unsupported driver capability "attributed_input"') });
    const client = connectable(daemon);
    client.driver({ ...baseDriver, deliver: () => accepted(), attributedInput: true });

    await expect(client.connect()).rejects.toThrow(/does not support driver capability "attributed_input".*13595cac/);
  });

  test("answers health from the driver", async () => {
    const daemon = await startDaemon();
    const client = connectable(daemon);
    client.driver({ ...baseDriver, health: () => ({ ok: false, message: "sample is not installed" }) });
    await client.connect();

    expect((await daemon.call("attn.health", {})).result).toEqual({ ok: false, message: "sample is not installed" });
  });

  test("refuses an invalid driver at the call site", () => {
    const client = new AttnPluginClient({ socketPath: "/unused", name: "p", version: "1" });
    expect(() => client.driver({ ...baseDriver, resumeAvailable: () => [] })).toThrow(/requires resume/);
    client.driver(baseDriver);
    expect(() => client.driver({ ...baseDriver, id: "other" })).toThrow(/already registers/);
  });
});

function connectable(daemon: FakeDaemon): AttnPluginClient {
  const client = new AttnPluginClient({ socketPath: daemon.socketPath, name: "sample-plugin", version: "0.1.0" });
  cleanups.push(async () => client.close());
  return client;
}

async function startDaemon(
  replies: Record<string, unknown> = {},
  beforeReply: (message: Message, socket: Socket) => void = () => {},
): Promise<FakeDaemon> {
  const root = await mkdtemp(join(tmpdir(), "attn-driver-sdk-"));
  const socketPath = join(root, "plugin.sock");
  const requests: Message[] = [];
  const waiting = new Map<number, (message: Message) => void>();
  const responses = new Map<number, Message>();
  const arrivals: { method: string; resolve: (message: Message) => void }[] = [];
  let plugin: Socket | undefined;
  let nextID = 1000;

  const server: Server = createServer((socket) => {
    plugin = socket;
    let buffer = "";
    socket.setEncoding("utf8");
    socket.on("data", (chunk) => {
      buffer += chunk;
      for (let newline = buffer.indexOf("\n"); newline !== -1; newline = buffer.indexOf("\n")) {
        const message = JSON.parse(buffer.slice(0, newline)) as Message;
        buffer = buffer.slice(newline + 1);
        if (!message.method) {
          responses.set(message.id!, message);
          waiting.get(message.id!)?.(message);
          continue;
        }
        requests.push(message);
        beforeReply(message, socket);
        const reply = replies[message.method] ?? (message.method === "hello" ? { ok: true, active_runs: [] } : { ok: true });
        const answer: Message = reply instanceof Error
          ? { jsonrpc: "2.0", id: message.id, error: { code: -32603, message: reply.message } }
          : { jsonrpc: "2.0", id: message.id, result: reply };
        socket.write(`${JSON.stringify(answer)}\n`);
        const index = arrivals.findIndex((arrival) => arrival.method === message.method);
        if (index !== -1) {
          arrivals.splice(index, 1)[0]!.resolve(message);
        }
      }
    });
  });
  await new Promise<void>((resolve) => server.listen(socketPath, resolve));
  cleanups.push(async () => {
    plugin?.destroy();
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await rm(root, { recursive: true, force: true });
  });

  return {
    socketPath,
    requests,
    response: (id) => new Promise((resolve) => {
      const seen = responses.get(id);
      if (seen) resolve(seen);
      else waiting.set(id, resolve);
    }),
    call: (method, params) =>
      new Promise((resolve) => {
        const id = nextID++;
        waiting.set(id, resolve);
        plugin!.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
      }),
    next: (method) =>
      new Promise((resolve) => {
        const seen = requests.find((request) => request.method === method);
        if (seen) {
          resolve(seen);
          return;
        }
        arrivals.push({ method, resolve });
      }),
  };
}
