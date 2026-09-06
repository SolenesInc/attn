import { existsSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { NetworkProxy, networkPolicyFrom, type NetworkDecision, type NetworkPolicy, type NetworkRequest } from "../netproxy";
import { availableModels, type AvailableModels, type ModelQuery } from "./models";
import type { AttnRPCClient } from "./attn-rpc";
import type { RelayConnection, RelayDelegate, RelayServer } from "./relay";
import type {
  RelayDeliverMessageParams,
  RelayDeliverMessageResult,
  RelayHelloParams,
  RelayHelloState,
  RelayHelloResult,
  RelayNetworkDecideParams,
  RelayNetworkDecideResult,
  RelayReportDenialParams,
  RelayReportExecPolicyAmendmentParams,
  RelayReportInputTakenParams,
  RelayReportNetworkAmendmentParams,
  RelayReportPullRequestParams,
  RelayReportSessionFileParams,
  RelayReportStateParams,
  RelayReportStopParams,
} from "./relay-protocol";
import { relayMethods } from "./relay-protocol";
import {
  compareVersion,
  evaluatePiVersion,
  parseStableVersion,
  piThinkingLevels,
  type ActivePluginRun,
  type DriverRegisterResult,
  type DriverSpawnParams,
  type DriverSpawnResult,
  type PiMetadata,
  type SessionClosedParams,
} from "./types";

export type CommandResult = { exitCode: number; stdout: string; stderr: string };
export type RunCommand = (argv: string[]) => Promise<CommandResult>;

type Availability =
  | { ok: true; executable: string; version: string }
  | { ok: false; message: string };

type RunState = {
  token: string;
  /** Proxy credentials, distinct from `token`: these reach the sandboxed command in its
   * environment, and the relay token must never be reachable from inside the sandbox. */
  proxyCredentials?: string;
  sessionID: string;
  runID: string;
  seq: number;
  metadata: PiMetadata;
  connection?: RelayConnection;
  /** Pending "nobody is declaring this session's state" alarm; see markUnbacked. */
  unbacked?: ReturnType<typeof setTimeout>;
};

const deliverMessageTimeoutMs = 10_000;

// Every pi session in this profile shares one proxy; each run's proxy credentials are
// how a held connection finds the session that made it.
type ProxyState = { proxy: NetworkProxy; address: { host: string; port: number } };

// A tripwire, not a deadline: a live pi re-dials within a second of the socket
// appearing and the suite's reconnect backoff caps at 30s (suite/core.ts).
const unbackedRunGraceMs = 120_000;

const defaultRunCommand: RunCommand = async (argv) => {
  const child = Bun.spawn(argv, { stdout: "pipe", stderr: "pipe" });
  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  return { exitCode, stdout, stderr };
};

export class PiDriver implements RelayDelegate {
  private readonly rpc: AttnRPCClient;
  private readonly runCommand: RunCommand;
  private readonly env: Record<string, string | undefined>;
  private readonly queryModels: ModelQuery;
  private modelQuery?: Promise<AvailableModels>;
  private readonly executable: string;
  private readonly relay: RelayServer;
  private readonly suitePath: string;
  private availability: Availability = { ok: false, message: "pi availability has not been checked" };
  private readonly runsByToken = new Map<string, RunState>();
  private readonly runsBySessionID = new Map<string, RunState>();
  private readonly runsByProxyCredentials = new Map<string, RunState>();

  /** The shipped tripwire, shortened by tests that would otherwise wait it out. */
  private readonly unbackedGraceMs: number;

  private readonly proxyStateDir: string | undefined;
  private proxyStart: Promise<ProxyState | undefined> | undefined;
  private proxyState: ProxyState | undefined;

  constructor(options: {
    rpc: AttnRPCClient;
    relay: RelayServer;
    suitePath: string;
    runCommand?: RunCommand;
    env?: Record<string, string | undefined>;
    queryModels?: ModelQuery;
    executable?: string;
    unbackedGraceMs?: number;
    proxyStateDir?: string;
  }) {
    this.rpc = options.rpc;
    this.relay = options.relay;
    this.suitePath = options.suitePath;
    this.runCommand = options.runCommand ?? defaultRunCommand;
    this.env = options.env ?? process.env;
    this.queryModels = options.queryModels ?? availableModels;
    this.executable = options.executable?.trim() || process.env.ATTN_PI_EXECUTABLE?.trim() || "pi";
    this.unbackedGraceMs = options.unbackedGraceMs ?? unbackedRunGraceMs;
    this.proxyStateDir = options.proxyStateDir?.trim() || this.env.ATTN_PLUGIN_DATA_ROOT?.trim() || undefined;
  }

  async initialize(): Promise<void> {
    await this.refreshAvailability();
    if (!this.availability.ok) return;
    const result = await this.rpc.request<DriverRegisterResult>("driver.register", {
      agent: "pi",
      capabilities: {
        resume: true,
        initial_prompt: true,
        model_pin: true,
        model_discovery: true,
        effort_pin: true,
        state_reporting: true,
        message_delivery: true,
        auto_mode: true,
        pull_request_reporting: true,
      },
    });
    if (!result.ok) throw new Error("attn rejected pi driver registration");
    // Adopt before listen(): the socket opens only once every inherited token is
    // known, so a suite re-dialing the instant the path appears is never refused.
    this.adoptActiveRuns(result.active_runs ?? []);
    // The proxy comes back on its persisted port before any suite re-dials, so an
    // inherited session's next network call is held for a decision, not refused.
    await this.ensureProxy(result.auto_mode);
    await this.relay.listen();
  }

  models(): Promise<AvailableModels> {
    return this.modelQuery ??= this.queryModels(this.executable, this.env).finally(() => {
      this.modelQuery = undefined;
    });
  }

  async delegationModels(): Promise<{ models: unknown[]; detail: string }> {
    const catalog = await this.models();
    if (catalog.problem) throw new Error(catalog.problem);
    return {
      models: catalog.providers.flatMap(provider => provider.models.map(model => ({
        harness: "pi", provider: provider.provider, id: model.id,
        name: model.name ?? model.id, description: "",
        effort_support: model.effortSupport ?? "unknown", effort_levels: model.effortLevels ?? [],
        access: provider.ready ? "unknown" : "unsupported", detail: provider.detail ?? "",
      }))),
      detail: "Configured Pi models. Account access is checked by the provider when used.",
    };
  }

  health(): { ok: boolean; message: string } {
    return this.availability.ok
      ? { ok: true, message: `pi ${this.availability.version} is ready` }
      : { ok: false, message: this.availability.message };
  }

  async spawn(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const suitePath = this.requireSuitePath();
    const metadata: PiMetadata = {
      schema: 1,
      pi_session_id: randomUUID(),
      pi_version: availability.version,
      model: cleanOptional(params.model),
      thinking: thinkingFor(params.effort),
    };
    const run = this.createRun(requireText(params.session_id, "session_id"), requireText(params.run_id, "run_id"), metadata);
    await this.reportMetadata(run);
    return {
      argv: this.argvFor(availability.executable, metadata, params.initial_prompt, suitePath),
      cwd: params.cwd,
      env: await this.envFor(run, params.auto_mode),
    };
  }

  async resume(params: DriverSpawnParams): Promise<DriverSpawnResult> {
    const availability = await this.requireAvailability();
    const suitePath = this.requireSuitePath();
    const requested = cleanOptional(params.resume_session_id);
    const previous = params.metadata === undefined ? undefined : parsePiMetadata(params.metadata);
    if (previous === undefined && requested === undefined) {
      throw new Error("resume needs the session's pi metadata or a resume_session_id naming the pi session to pick up");
    }
    if (previous !== undefined) {
      const installed = parseStableVersion(availability.version);
      const recorded = parseStableVersion(previous.pi_version);
      if (compareVersion(installed, recorded) < 0) {
        throw new Error(
          `installed pi ${installed.raw} is older than the ${recorded.raw} this session last ran on; upgrade pi or point ATTN_PI_EXECUTABLE at a matching build`,
        );
      }
    }
    // An explicit id is attn pointing this session at a conversation (a seed's
    // saved resume, a reopened session); it outranks what the row last held.
    const metadata: PiMetadata = {
      schema: 1,
      pi_session_id: requested ?? previous!.pi_session_id,
      pi_version: availability.version,
      model: cleanOptional(params.model) ?? previous?.model,
      thinking: thinkingFor(params.effort) ?? previous?.thinking,
    };
    const run = this.createRun(requireText(params.session_id, "session_id"), requireText(params.run_id, "run_id"), metadata);
    await this.reportMetadata(run);
    return {
      argv: this.argvFor(availability.executable, metadata, undefined, suitePath),
      cwd: params.cwd,
      env: await this.envFor(run, params.auto_mode),
    };
  }

  async sessionClosed(params: SessionClosedParams): Promise<{ ok: true }> {
    const run = this.runsBySessionID.get(params.session_id);
    // A close for a run this session has already replaced is late news; acting on it
    // would revoke the successor's credentials out from under a live session.
    if (run && run.runID === params.run_id) {
      this.markBacked(run);
      this.runsBySessionID.delete(params.session_id);
      this.runsByToken.delete(run.token);
      this.forgetProxyCredentials(run);
    }
    return { ok: true };
  }

  private forgetProxyCredentials(run: RunState): void {
    if (!run.proxyCredentials) return;
    this.runsByProxyCredentials.delete(run.proxyCredentials);
    this.proxyState?.proxy.revokeCredentials(run.proxyCredentials);
  }

  /** Shuts the profile's proxy down; the driver process owns it for as long as it runs. */
  async close(): Promise<void> {
    const running = this.proxyStart === undefined ? undefined : await this.proxyStart;
    this.proxyStart = undefined;
    this.proxyState = undefined;
    await running?.proxy.close();
  }

  /** The listening proxy, for tests and diagnostics; undefined until a spawn asks for one. */
  networkProxy(): NetworkProxy | undefined {
    return this.proxyState?.proxy;
  }


  async suiteHello(connection: RelayConnection, rawParams: unknown): Promise<RelayHelloResult> {
    const params = parseRelayHello(rawParams);
    const run = this.requireRunByToken(params.token);
    this.adoptProxyCredentials(run, params.proxy_credentials);
    run.connection = connection;
    this.markBacked(run);
    if (params.dropped_reports !== undefined) {
      console.error(
        `attn-pi: session ${run.sessionID} could not deliver ${params.dropped_reports} state report(s) while the relay was down`,
      );
    }
    connection.onClose(() => {
      if (run.connection !== connection) return;
      run.connection = undefined;
      console.error(
        `attn-pi: relay connection for session ${run.sessionID} closed; nothing declares its state until a suite dials back`,
      );
      this.markUnbacked(run, "the pi suite disconnected");
    });
    run.metadata = { ...run.metadata, pi_session_id: params.pi_session_id, pi_version: params.pi_version };
    await this.reportMetadata(run);
    if (params.pi_state !== undefined) await this.restateAfterUnknown(run, params.pi_state);
    return { ok: true };
  }

  /** An adopted run learns its proxy credentials from the suite that still holds them.
   * A spawn-minted value always wins: only the driver may hand out new credentials. */
  private adoptProxyCredentials(run: RunState, offered: string | undefined): void {
    if (offered === undefined || offered === "") return;
    if (run.proxyCredentials !== undefined) {
      if (run.proxyCredentials !== offered) {
        console.error(
          `attn-pi: session ${run.sessionID} said hello with proxy credentials this driver did not mint; keeping the ones it did`,
        );
      }
      return;
    }
    run.proxyCredentials = offered;
    this.runsByProxyCredentials.set(offered, run);
    this.proxyState?.proxy.registerCredentials(offered);
  }

  /** Hands attn what pi says it is, to use only while attn says `unknown`: a hello
   * is news about the channel, and declaring it would re-open a settled turn. */
  private async restateAfterUnknown(run: RunState, state: RelayHelloState): Promise<void> {
    try {
      await this.rpc.request("session.report_state", {
        session_id: run.sessionID,
        run_id: run.runID,
        seq: this.nextSeq(run),
        state,
        only_if_unknown: true,
      });
    } catch (error) {
      console.error(`attn-pi: could not restate session ${run.sessionID} as ${state}: ${String(error)}`);
    }
  }

  async suiteReportState(rawParams: unknown): Promise<void> {
    const params = parseRelayReportState(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_state", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      state: params.state,
    });
  }

  async suiteReportStop(rawParams: unknown): Promise<void> {
    const params = parseRelayReportStop(rawParams);
    const run = this.requireRunByToken(params.token);
    const text = params.assistant_text.trim();
    // Reserve the seq BEFORE awaiting classification: a message delivered mid-
    // classification starts a turn whose report must outrank this stop.
    const seq = this.nextSeq(run);
    const verdict = text === "" || params.aborted ? "idle" : await this.classifyStop(run, text);
    await this.rpc.request("session.report_stop", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq,
      verdict,
    });
  }

  async suiteReportDenial(rawParams: unknown): Promise<void> {
    const params = parseRelayReportDenial(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_automode_denial", {
      session_id: run.sessionID,
      run_id: run.runID,
      tool: params.tool,
      action: params.action,
      reason: params.reason,
      rule: params.rule,
      at: params.at,
    });
  }

  async suiteReportExecPolicyAmendment(rawParams: unknown): Promise<void> {
    const params = parseRelayReportExecPolicyAmendment(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_execpolicy_amendment", {
      session_id: run.sessionID,
      run_id: run.runID,
      pattern: params.pattern,
      decision: params.decision,
      justification: params.justification ?? "",
    });
  }

  async suiteReportNetworkAmendment(rawParams: unknown): Promise<void> {
    const params = parseRelayReportNetworkAmendment(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_network_amendment", {
      session_id: run.sessionID,
      run_id: run.runID,
      host: params.host,
      decision: params.decision,
    });
  }

  async suiteReportInputTaken(rawParams: unknown): Promise<void> {
    const params = parseRelayReportInputTaken(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_input_taken", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      input_id: params.input_id,
    });
  }

  async suiteReportPullRequest(rawParams: unknown): Promise<void> {
    const params = parseRelayReportPullRequest(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_pull_request", {
      session_id: run.sessionID,
      run_id: run.runID,
      url: params.url,
    });
  }

  async suiteReportSessionFile(rawParams: unknown): Promise<void> {
    const params = parseRelayReportSessionFile(rawParams);
    const run = this.requireRunByToken(params.token);
    await this.rpc.request("session.report_transcript_path", {
      session_id: run.sessionID,
      run_id: run.runID,
      path: params.path,
    });
  }

  async deliverMessage(rawParams: unknown): Promise<{ ok: boolean }> {
    const params = parseDeliverMessageParams(rawParams);
    const run = this.runsBySessionID.get(params.session_id);
    if (!run) throw new Error(`no active pi run for session ${params.session_id}`);
    if (run.runID !== params.run_id) {
      throw new Error(`run_id mismatch for session ${params.session_id}: expected ${run.runID}, got ${params.run_id}`);
    }
    if (!run.connection) throw new Error(`no live pi suite connection for session ${params.session_id}`);
    const result = await this.relay.deliverMessage<RelayDeliverMessageParams, RelayDeliverMessageResult>(
      run.connection,
      { input_id: params.input_id, text: params.text },
      deliverMessageTimeoutMs,
    );
    return { ok: result.delivered };
  }

  private async classifyStop(run: RunState, assistantText: string): Promise<string> {
    const result = await this.rpc.request<{ verdict: string }>("attn.classify_stop", {
      session_id: run.sessionID,
      run_id: run.runID,
      assistant_text: assistantText,
    });
    return result.verdict;
  }

  private createRun(sessionID: string, runID: string, metadata: PiMetadata): RunState {
    const previous = this.runsBySessionID.get(sessionID);
    if (previous) {
      this.runsByToken.delete(previous.token);
      // The replaced run's grants and credentials go with it: a relaunched session must
      // not inherit what a reviewer allowed for the run before it.
      this.forgetProxyCredentials(previous);
    }
    const run: RunState = { token: runID, proxyCredentials: randomUUID(), sessionID, runID, seq: 0, metadata };
    this.runsByToken.set(run.token, run);
    if (run.proxyCredentials) this.runsByProxyCredentials.set(run.proxyCredentials, run);
    this.runsBySessionID.set(sessionID, run);
    this.markUnbacked(run, "the pi suite has not connected since this run was launched");
    return run;
  }

  /** Rebuilds this driver's run state from the runs attn reports still live.
   * Pi metadata is the discriminator for runs this driver can adopt. */
  private adoptActiveRuns(runs: ActivePluginRun[]): void {
    for (const run of runs) {
      let metadata: PiMetadata;
      try {
        metadata = parsePiMetadata(run.metadata);
      } catch {
        continue;
      }
      const seq = run.seq;
      if (typeof seq !== "number" || !Number.isSafeInteger(seq) || seq < 0) {
        console.error(
          `attn-pi: not adopting run ${run.run_id} for session ${run.session_id}: driver.register carried no report cursor, so this session's state will not move until it is relaunched`,
        );
        continue;
      }
      const state: RunState = {
        token: run.run_id,
        sessionID: run.session_id,
        runID: run.run_id,
        seq,
        metadata,
      };
      this.runsByToken.set(state.token, state);
      this.runsBySessionID.set(state.sessionID, state);
      this.markUnbacked(state, "adopted from attn, waiting for its pi suite to re-dial");
    }
  }

  /** Starts the grace for a run nothing is declaring state for. On expiry the driver
   * says `unknown` rather than leaving a stale declaration standing. */
  private markUnbacked(run: RunState, why: string): void {
    this.markBacked(run);
    const timer = setTimeout(() => {
      run.unbacked = undefined;
      void this.declareUnbacked(run, why);
    }, this.unbackedGraceMs);
    // A pending alarm must not hold up the runtime's exit with its daemon connection.
    timer.unref?.();
    run.unbacked = timer;
  }

  private markBacked(run: RunState): void {
    if (run.unbacked === undefined) return;
    clearTimeout(run.unbacked);
    run.unbacked = undefined;
  }

  private async declareUnbacked(run: RunState, why: string): Promise<void> {
    console.error(
      `attn-pi: no pi suite for session ${run.sessionID} for ${this.unbackedGraceMs}ms (${why}); reporting unknown so attn stops showing a state nobody is refreshing`,
    );
    try {
      await this.rpc.request("session.report_state", {
        session_id: run.sessionID,
        run_id: run.runID,
        seq: this.nextSeq(run),
        state: "unknown",
      });
    } catch (error) {
      console.error(`attn-pi: could not report unknown for session ${run.sessionID}: ${String(error)}`);
    }
  }

  private requireRunByToken(token: string): RunState {
    const run = this.runsByToken.get(token);
    if (!run) throw new Error("unknown pi suite token");
    return run;
  }

  private nextSeq(run: RunState): number {
    run.seq += 1;
    return run.seq;
  }

  private requireSuitePath(): string {
    if (!existsSync(this.suitePath)) {
      throw new Error(`pi suite entrypoint not found at ${this.suitePath}; this is a build/packaging bug`);
    }
    return this.suitePath;
  }

  // The auto-mode config travels in the environment, not argv: argv is
  // world-readable and prose entries are multi-line.
  private async envFor(run: RunState, autoMode: unknown): Promise<Record<string, string>> {
    const env: Record<string, string> = { ATTN_PI_SUITE_SOCKET: this.relay.socketPath, ATTN_PI_TOKEN: run.token };
    if (autoMode !== undefined && autoMode !== null) {
      env.ATTN_PI_AUTOMODE_CONFIG = JSON.stringify(autoMode);
      const ledger = process.env.ATTN_AUTOMODE_DENIAL_LOG?.trim();
      if (ledger) env.ATTN_PI_AUTOMODE_DENIAL_LOG = ledger;
      env.ATTN_PI_SESSION_ID = run.sessionID;
    }
    const proxy = await this.ensureProxy(autoMode);
    if (proxy && run.proxyCredentials) {
      proxy.proxy.registerCredentials(run.proxyCredentials);
      env.ATTN_PI_PROXY_ADDR = `${proxy.address.host}:${proxy.address.port}`;
      env.ATTN_PI_PROXY_CREDENTIALS = run.proxyCredentials;
    }
    return env;
  }

  /** Starts the profile's proxy on the first spawn that asks for network policy, and
   * pushes every later spawn's policy onto the one already listening. */
  private async ensureProxy(autoMode: unknown): Promise<ProxyState | undefined> {
    const policy = networkPolicyFrom(autoMode);
    if (this.proxyStart) {
      const running = await this.proxyStart;
      if (running && policy) running.proxy.setPolicy(policy);
      return running;
    }
    if (!policy?.enabled) return undefined;
    this.proxyStart = this.startProxy(policy);
    this.proxyState = await this.proxyStart;
    return this.proxyState;
  }

  private async startProxy(policy: NetworkPolicy): Promise<ProxyState | undefined> {
    const stateDir = this.proxyStateDir;
    if (!stateDir) {
      console.error(
        "attn-pi: auto mode asked for network policy but ATTN_PLUGIN_DATA_ROOT is unset, so no proxy was started and sessions get no ATTN_PI_PROXY_ADDR",
      );
      return undefined;
    }
    const proxy = new NetworkProxy({ policy, stateDir, decide: (request) => this.decideNetwork(request) });
    try {
      return { proxy, address: await proxy.listen() };
    } catch (error) {
      console.error(`attn-pi: could not start the network proxy in ${stateDir}: ${String(error)}`);
      return undefined;
    }
  }

  // A held connection is decided by the session that made it: the proxy credentials name
  // that run, and the answer comes back over that run's own relay connection.
  private async decideNetwork(request: NetworkRequest): Promise<NetworkDecision> {
    const run = this.runsByProxyCredentials.get(request.credentials);
    const connection = run?.connection;
    if (!connection) {
      throw new Error(`no live pi suite for these proxy credentials; nothing can decide ${request.host}`);
    }
    const params: RelayNetworkDecideParams = { host: request.host, port: request.port, protocol: request.protocol };
    return this.relay.networkDecide<RelayNetworkDecideParams, RelayNetworkDecideResult>(connection, params);
  }

  /** attn pushes a network policy change here so the running proxy picks it up without
   * waiting for the next spawn. Sessions with no proxy yet read it from their config. */
  async policyChanged(rawParams: unknown): Promise<{ ok: true }> {
    const policy = networkPolicyFrom(rawParams);
    if (!policy) throw new Error("automode.policy_changed params must carry a network object");
    const running = this.proxyStart === undefined ? undefined : await this.proxyStart;
    running?.proxy.setPolicy(policy);
    return { ok: true };
  }

  private argvFor(
    executable: string,
    metadata: PiMetadata,
    initialPrompt: string | undefined,
    suitePath: string,
  ): string[] {
    const argv = [executable, "--session-id", metadata.pi_session_id];
    if (metadata.model) argv.push("--model", metadata.model);
    if (metadata.thinking) argv.push("--thinking", metadata.thinking);
    argv.push("-e", suitePath);
    if (initialPrompt !== undefined && initialPrompt.trim() !== "") argv.push(initialPrompt);
    return argv;
  }

  /** The pi session id doubles as attn's resume identity for the session, so a
   * closed session or a seed can find its way back to this conversation. */
  private async reportMetadata(run: RunState): Promise<void> {
    await this.rpc.request("session.report_metadata", {
      session_id: run.sessionID,
      run_id: run.runID,
      seq: this.nextSeq(run),
      metadata: run.metadata,
      resume_session_id: run.metadata.pi_session_id,
    });
  }

  private async refreshAvailability(): Promise<void> {
    try {
      const result = await this.runCommand([this.executable, "--version"]);
      if (result.exitCode !== 0) throw new Error(result.stderr.trim() || `exit ${result.exitCode}`);
      const evaluated = evaluatePiVersion(result.stdout.trim());
      if (evaluated.kind === "invalid") throw new Error(`unrecognized pi version: ${evaluated.reason}`);
      if (evaluated.kind === "too_old") {
        throw new Error(`pi ${evaluated.installed.raw} is older than the minimum supported ${evaluated.minimum.raw}`);
      }
      this.availability = { ok: true, executable: this.executable, version: evaluated.installed.raw };
    } catch (error) {
      this.availability = { ok: false, message: `pi executable ${this.executable} is unavailable: ${safeError(error)}` };
    }
  }

  private async requireAvailability(): Promise<Extract<Availability, { ok: true }>> {
    await this.refreshAvailability();
    if (!this.availability.ok) throw new Error(this.availability.message);
    return this.availability;
  }
}

export function parsePiMetadata(value: unknown): PiMetadata {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("pi session metadata must be an object");
  }
  const record = value as Record<string, unknown>;
  if (record.schema !== 1) throw new Error(`unsupported pi session metadata schema ${JSON.stringify(record.schema)}`);
  const sessionID = record.pi_session_id;
  if (typeof sessionID !== "string" || sessionID.trim() === "") throw new Error("pi session metadata is missing pi_session_id");
  const version = record.pi_version;
  if (typeof version !== "string" || version.trim() === "") throw new Error("pi session metadata is missing pi_version");
  return {
    schema: 1,
    pi_session_id: sessionID.trim(),
    pi_version: version.trim(),
    model: cleanOptional(typeof record.model === "string" ? record.model : undefined),
    thinking: cleanOptional(typeof record.thinking === "string" ? record.thinking : undefined),
  };
}

function parseRelayHello(value: unknown): RelayHelloParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.hello params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const piSessionID = record.pi_session_id;
  const piVersion = record.pi_version;
  const reason = record.reason;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.hello is missing token");
  if (typeof piSessionID !== "string" || piSessionID.trim() === "") throw new Error("suite.hello is missing pi_session_id");
  if (typeof piVersion !== "string" || piVersion.trim() === "") throw new Error("suite.hello is missing pi_version");
  if (typeof reason !== "string") throw new Error("suite.hello is missing reason");
  const dropped = record.dropped_reports;
  const piState = record.pi_state;
  if (piState !== undefined && piState !== "idle" && piState !== "working" && piState !== "pending_approval") {
    throw new Error(`suite.hello has unsupported pi_state ${String(piState)}`);
  }
  return {
    token: token.trim(),
    pi_session_id: piSessionID.trim(),
    pi_version: piVersion.trim(),
    reason,
    // A suite too old to send it says nothing, which is not the same as zero.
    // Only a positive count is reported.
    dropped_reports: typeof dropped === "number" && Number.isFinite(dropped) && dropped > 0 ? dropped : undefined,
    pi_state: piState,
    proxy_credentials: typeof record.proxy_credentials === "string" ? record.proxy_credentials.trim() : undefined,
  };
}

function parseRelayReportState(value: unknown): RelayReportStateParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_state params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_state is missing token");
  if (record.state !== "working" && record.state !== "pending_approval") {
    throw new Error(
      `suite.report_state state must be "working" or "pending_approval", got ${JSON.stringify(record.state)}`,
    );
  }
  return { token: token.trim(), state: record.state };
}

function parseRelayReportStop(value: unknown): RelayReportStopParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_stop params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const assistantText = record.assistant_text;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_stop is missing token");
  if (typeof assistantText !== "string") throw new Error("suite.report_stop is missing assistant_text");
  const aborted = record.aborted;
  if (aborted !== undefined && typeof aborted !== "boolean") throw new Error("suite.report_stop aborted must be a boolean");
  return { token: token.trim(), assistant_text: assistantText, aborted: aborted === true };
}

function parseRelayReportDenial(value: unknown): RelayReportDenialParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_denial params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_denial is missing token");
  const action = record.action;
  if (typeof action !== "string" || action.trim() === "") throw new Error("suite.report_denial is missing action");
  return {
    token: token.trim(),
    tool: textField(record.tool),
    action: action.trim(),
    reason: textField(record.reason),
    rule: textField(record.rule),
    at: textField(record.at),
  };
}

function parseRelayReportPullRequest(value: unknown): RelayReportPullRequestParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_pull_request params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const url = record.url;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_pull_request is missing token");
  if (typeof url !== "string" || url.trim() === "") throw new Error("suite.report_pull_request is missing url");
  return { token: token.trim(), url: url.trim() };
}

function parseRelayReportSessionFile(value: unknown): RelayReportSessionFileParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_session_file params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const path = record.path;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_session_file is missing token");
  if (typeof path !== "string" || path.trim() === "") throw new Error("suite.report_session_file is missing path");
  return { token: token.trim(), path: path.trim() };
}

function parseRelayReportExecPolicyAmendment(value: unknown): RelayReportExecPolicyAmendmentParams {
  const record = objectParams(value, relayMethods.reportExecPolicyAmendment);
  const token = tokenField(record.token, relayMethods.reportExecPolicyAmendment);
  const pattern = record.pattern;
  if (!Array.isArray(pattern) || pattern.length === 0) {
    throw new Error(`${relayMethods.reportExecPolicyAmendment} pattern must be a non-empty list of command tokens`);
  }
  const tokens = pattern.map((entry, index) => {
    if (typeof entry !== "string" || entry.trim() === "") {
      throw new Error(`${relayMethods.reportExecPolicyAmendment} pattern[${index}] must be a non-empty string`);
    }
    return entry.trim();
  });
  const decision = textField(record.decision);
  if (decision === "") throw new Error(`${relayMethods.reportExecPolicyAmendment} is missing decision`);
  return { token, pattern: tokens, decision, justification: textField(record.justification) };
}

function parseRelayReportNetworkAmendment(value: unknown): RelayReportNetworkAmendmentParams {
  const record = objectParams(value, relayMethods.reportNetworkAmendment);
  const token = tokenField(record.token, relayMethods.reportNetworkAmendment);
  const host = textField(record.host);
  if (host === "") throw new Error(`${relayMethods.reportNetworkAmendment} is missing host`);
  const decision = textField(record.decision);
  if (decision === "") throw new Error(`${relayMethods.reportNetworkAmendment} is missing decision`);
  return { token, host, decision };
}

function objectParams(value: unknown, method: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null) throw new Error(`${method} params must be an object`);
  return value as Record<string, unknown>;
}

function tokenField(value: unknown, method: string): string {
  if (typeof value !== "string" || value.trim() === "") throw new Error(`${method} is missing token`);
  return value.trim();
}

function textField(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function parseDeliverMessageParams(value: unknown): { session_id: string; run_id: string; input_id: string; text: string } {
  if (typeof value !== "object" || value === null) throw new Error("driver.deliver_message params must be an object");
  const record = value as Record<string, unknown>;
  const sessionID = record.session_id;
  const runID = record.run_id;
  const inputID = record.input_id;
  const text = record.text;
  if (typeof sessionID !== "string" || sessionID.trim() === "") throw new Error("driver.deliver_message is missing session_id");
  if (typeof runID !== "string" || runID.trim() === "") throw new Error("driver.deliver_message is missing run_id");
  if (typeof inputID !== "string" || inputID.trim() === "") throw new Error("driver.deliver_message is missing input_id");
  if (typeof text !== "string") throw new Error("driver.deliver_message is missing text");
  return { session_id: sessionID.trim(), run_id: runID.trim(), input_id: inputID.trim(), text };
}

function parseRelayReportInputTaken(value: unknown): RelayReportInputTakenParams {
  if (typeof value !== "object" || value === null) throw new Error("suite.report_input_taken params must be an object");
  const record = value as Record<string, unknown>;
  const token = record.token;
  const inputID = record.input_id;
  if (typeof token !== "string" || token.trim() === "") throw new Error("suite.report_input_taken is missing token");
  if (typeof inputID !== "string" || inputID.trim() === "") throw new Error("suite.report_input_taken is missing input_id");
  return { token: token.trim(), input_id: inputID.trim() };
}

function thinkingFor(effort: string | undefined): string | undefined {
  const cleaned = cleanOptional(effort);
  if (cleaned === undefined) return undefined;
  if (!(piThinkingLevels as readonly string[]).includes(cleaned)) {
    throw new Error(`unsupported pi thinking level ${JSON.stringify(cleaned)}; expected one of ${piThinkingLevels.join(", ")}`);
  }
  return cleaned;
}

function cleanOptional(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

function requireText(value: string, field: string): string {
  const trimmed = value?.trim();
  if (!trimmed) throw new Error(`${field} is required`);
  return trimmed;
}

function safeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
