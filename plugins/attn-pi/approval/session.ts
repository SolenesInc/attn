import { createLocalBashOperations, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { renderPrompt } from "../automode/prompt-catalog";
import { renderEnvironment } from "../automode/environment";
import type { DenialLedgerLike } from "../automode/ledger";
import { commandEnvironment, sandboxSpecFor, wrapCommand, type ProxyAddress, type SandboxConfig } from "../sandbox/index";
import type { Decider } from "../netproxy/index";
import { loadApprovalConfig, type ApprovalConfig, type ApprovalPolicy, type RawApprovalConfig, type SandboxMode } from "./config";
import { GuardianReviewer, type GuardianUsageEntry } from "./guardian";
import { guardianSettings, readGuardianSelection, resolveGuardian, type GuardianControl, type GuardianSelection } from "./guardian-selection";
import { ApprovalOrchestrator, type OrchestratorDenial, type SandboxSource } from "./orchestrator";
import { pickPreset } from "./permissions-ui";
import { describePermissions, presetByID, presetFor, type Preset } from "./presets";
import { compileRules } from "./rules";
import { UserReviewer } from "./reviewers";
import { transcriptFromSession, truncateForGuardian, maxToolEntryTokens } from "./transcript";
import type { Reviewer } from "./types";

export const approvalConfigEnvVar = "ATTN_PI_AUTOMODE_CONFIG";
export const statusKey = "attn-auto";

export type Permissions = { approvalPolicy: ApprovalPolicy; sandboxMode: SandboxMode };

export type ApprovalSuiteLike = {
  networkDecider: Decider | undefined;
  acquireCommandProxy(proxy: ProxyAddress, signal?: AbortSignal): Promise<{ proxy: ProxyAddress; release: () => void }>;
  reportDenial(denial: { tool: string; action: string; reason: string; rule: string; at: string }): void;
  reportApprovalWindow(open: boolean): void;
  reportExecPolicyAmendment(amendment: { pattern: string[]; decision: string; justification?: string }): Promise<void>;
  reportNetworkAmendment(amendment: { host: string; decision: string }): Promise<void>;
};

export type ApprovalSetup = {
  config: ApprovalConfig;
  suite: ApprovalSuiteLike;
  ledger: DenialLedgerLike;
  proxy?: ProxyAddress;
  notice?: string;
};

export type ApprovalSource = { config: ApprovalConfig; problem?: string };

/** The paths the session's security policy contributes to the sandbox, not carried by the daemon's approval config. */
export type SandboxPaths = {
  cwd: string;
  temp: string;
  allowWrite: string[];
  denyRead: string[];
  denyWrite: string[];
  cacheWritePaths: string[];
};

export function attnApprovalSource(env: Record<string, string | undefined>): ApprovalSource | undefined {
  const raw = env[approvalConfigEnvVar]?.trim();
  if (!raw) return undefined;
  try {
    return { config: loadApprovalConfig(JSON.parse(raw) as RawApprovalConfig) };
  } catch (error) {
    return {
      config: loadApprovalConfig(undefined),
      problem: `attn sent an approval config this session could not read: ${message(error)}. ` +
        `Approvals are running on their shipped defaults.`,
    };
  }
}

export function proxyFromEnvironment(env: Record<string, string | undefined>): ProxyAddress | undefined {
  const address = env.ATTN_PI_PROXY_ADDR?.trim();
  const credentials = env.ATTN_PI_PROXY_CREDENTIALS?.trim();
  if (!address || !credentials) return undefined;
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  if (!Number.isInteger(port) || port <= 0) return undefined;
  return { host: "127.0.0.1", port, credentials };
}

export class PiApproval {
  private readonly orchestrator: ApprovalOrchestrator;
  private readonly user: UserReviewer;
  private guardian: GuardianReviewer | undefined;
  private paths: SandboxPaths | undefined;
  private choice: boolean | undefined;
  private guardianOverride: GuardianSelection | undefined;
  private flag: boolean | undefined;
  private context: ExtensionContext | undefined;
  private noticed = false;
  private readonly problems: string[];
  private pair: Permissions;
  private readonly listeners: ((pair: Permissions) => Promise<void> | void)[] = [];

  constructor(private readonly setup: ApprovalSetup) {
    this.pair = { approvalPolicy: setup.config.approvalPolicy, sandboxMode: setup.config.sandboxMode };
    const compiled = compileRules(setup.config);
    this.problems = compiled.problems;
    this.user = new UserReviewer({
      rules: () => this.orchestrator.rules(),
      onWaiting: (waiting) => setup.suite.reportApprovalWindow(waiting),
    });
    const local = createLocalBashOperations({ shellPath: "/bin/bash" });
    this.orchestrator = new ApprovalOrchestrator({
      approvalPolicy: () => this.pair.approvalPolicy,
      sandboxMode: () => this.pair.sandboxMode,
      sandbox: () => this.sandboxSource(),
      reviewer: () => this.reviewer(),
      rules: compiled.rules,
      ...(setup.proxy ? { proxy: setup.proxy, acquireProxy: (signal) => setup.suite.acquireCommandProxy(setup.proxy!, signal) } : {}),
      run: (command, cwd, options) => local.exec(command, cwd, options),
      onDenial: (denial) => this.record(denial),
      onExecPolicyAmendment: (pattern) =>
        this.amended("command", setup.suite.reportExecPolicyAmendment({ pattern, decision: "allow" })),
      onNetworkAmendment: (host) =>
        this.amended("host", setup.suite.reportNetworkAmendment({ host, decision: "allow" })),
      notify: (text, level) => this.context?.ui?.notify(text, level),
    });
  }

  useSandbox(paths: SandboxPaths | undefined): void {
    this.paths = paths;
  }

  /** The pair every later command runs under: attn's launch choice until /permissions changes it. */
  permissions(): Permissions {
    return { ...this.pair };
  }

  /** Resolves once every listener has caught up, so a caller only tells the user the
   * session changed after the tools the new pair governs are the ones registered. */
  async setPermissions(pair: Permissions): Promise<void> {
    const previous = this.pair;
    this.pair = { approvalPolicy: pair.approvalPolicy, sandboxMode: pair.sandboxMode };
    try {
      await this.announce(previous);
    } finally {
      if (this.context) this.paint(this.context);
    }
  }

  /** Security rebuilds the native file tools from here, so the pair governs them too. */
  onPermissions(listener: (pair: Permissions) => Promise<void> | void): void {
    this.listeners.push(listener);
  }

  private async announce(previous: Permissions): Promise<void> {
    if (previous.approvalPolicy === this.pair.approvalPolicy && previous.sandboxMode === this.pair.sandboxMode) return;
    await Promise.all(this.listeners.map((listener) => listener({ ...this.pair })));
  }

  readonly runBash = (...args: Parameters<ApprovalOrchestrator["runBash"]>) => this.orchestrator.runBash(...args);

  enabled(): boolean {
    return this.choice ?? this.flag ?? this.setup.config.enabledDefault;
  }

  reviewer(): Reviewer {
    return this.enabled() && this.guardian ? this.guardian : this.user;
  }

  register(pi: ExtensionAPI): void {
    pi.registerFlag("auto", { description: "Start with attn auto mode on", type: "boolean" });
    pi.registerFlag("no-auto", { description: "Start with attn auto mode off", type: "boolean" });
    pi.registerCommand("auto", {
      description: "Toggle attn auto mode (on | off | status)",
      handler: async (args, ctx) => this.command(args, ctx),
    });
    pi.registerCommand("permissions", {
      description: "Choose what the agent is allowed to do (read-only | default | full-access | untrusted | status)",
      handler: (args, ctx) => this.permissionsCommand(args, ctx),
    });
    pi.on("session_start", async (_event, ctx) => {
      // --no-auto wins a session given both; an unset flag reads as undefined.
      this.flag = pi.getFlag("no-auto") === true ? false : pi.getFlag("auto") === true ? true : undefined;
      // One PiApproval serves every session in this process, so a /reload or /new
      // drops what /permissions and /auto answered and starts from the daemon's launch choice.
      const launched = this.pair;
      this.pair = { approvalPolicy: this.setup.config.approvalPolicy, sandboxMode: this.setup.config.sandboxMode };
      this.choice = undefined;
      this.guardianOverride = undefined;
      await this.announce(launched);
      this.context = ctx;
      this.guardian = this.makeGuardian(pi, ctx);
      this.setup.suite.networkDecider = this.orchestrator.decideNetwork;
      this.paint(ctx);
      this.speak(ctx);
      await this.orchestrator.ready();
    });
    pi.on("agent_start", (_event, ctx) => {
      this.context = ctx;
      this.guardian?.startTurn();
    });
    pi.on("model_select", (_event, ctx) => { this.context = ctx; });
  }

  private makeGuardian(pi: ExtensionAPI, ctx: ExtensionContext): GuardianReviewer | undefined {
    const registry = ctx.modelRegistry;
    if (!registry) return undefined;
    const systemPrompt = renderPrompt(
      "system",
      { environment: renderEnvironment(this.setup.config.environment) },
      "pi-guardian",
    );
    return new GuardianReviewer({
      registry,
      model: () => this.context?.model,
      resolve: async () => {
        const selected = resolveGuardian(this.guardianOverride ?? this.setup.config.guardian, this.context!);
        const auth = await registry.getApiKeyAndHeaders(selected.model);
        if (!auth.ok) throw new Error(`Guardian ${selected.model.provider}/${selected.model.id}: ${auth.error}. Configure credentials or choose another model in /security.`);
        return selected;
      },
      systemPrompt: () => systemPrompt,
      transcript: () => transcriptFromSession(this.context?.sessionManager.buildContextEntries() ?? []),
      sessionId: () => this.context?.sessionManager.getSessionId() ?? "",
      runTool: (command, signal) => this.inspect(command, signal),
      onUsage: (entry: GuardianUsageEntry) => pi.appendEntry("attn-guardian-usage", entry),
      notify: (text, level) => this.context?.ui?.notify(text, level),
    });
  }

  private async inspect(command: string, signal: AbortSignal): Promise<{ output: string; isError: boolean }> {
    const source = this.sandboxSource();
    const config: SandboxConfig = { ...source.config, mode: "read-only", network: "off" };
    const spec = sandboxSpecFor(config, source.cwd, source.temp, { permissions: "use_default" });
    const local = createLocalBashOperations({ shellPath: "/bin/bash" });
    let output = "";
    const result = await local.exec(spec === "unsandboxed" ? command : wrapCommand(spec, command), source.cwd, {
      onData: (data) => { output += data.toString(); },
      signal,
      env: commandEnvironment(spec, process.env),
    });
    return { output: truncateForGuardian(output, maxToolEntryTokens), isError: result.exitCode !== 0 };
  }

  sandboxSource(): SandboxSource {
    const paths = this.paths;
    if (!paths) throw new Error("security has not configured a sandbox for this session yet");
    return {
      config: {
        mode: this.pair.sandboxMode,
        network: this.setup.config.network.enabled ? "proxy" : "off",
        allowWrite: paths.allowWrite,
        denyRead: paths.denyRead,
        denyWrite: paths.denyWrite,
        cacheWritePaths: paths.cacheWritePaths,
      },
      cwd: paths.cwd,
      temp: paths.temp,
    };
  }

  // The amendment and command already took effect; only future persistence failed, and the user hears about it.
  private amended(kind: string, reported: Promise<void>): void {
    void reported.catch((error: unknown) => {
      this.context?.ui?.notify(
        `attn did not record this ${kind} amendment: ${message(error)}. It holds for this session only.`,
        "error",
      );
    });
  }

  // The rejection already stands; a failed report just gets a warning, not a broken tool call.
  private record(denial: OrchestratorDenial): void {
    try {
      this.setup.ledger.record(denial);
    } catch (error) {
      this.context?.ui?.notify(`This rejection has no local record: ${message(error)}`, "error");
    }
    try {
      this.setup.suite.reportDenial(denial);
    } catch (error) {
      this.context?.ui?.notify(`attn was not told about this rejection: ${message(error)}`, "error");
    }
  }

  private command(args: string, ctx: ExtensionContext): void {
    const asked = args.trim().toLowerCase();
    if (asked === "on") this.choice = true;
    else if (asked === "off") this.choice = false;
    else if (asked === "" || asked === "toggle") this.choice = !this.enabled();
    else if (asked !== "status") {
      ctx.ui?.notify(`/auto takes on, off, status, or nothing at all, not ${JSON.stringify(asked)}.`, "error");
      return;
    }
    this.paint(ctx);
    ctx.ui?.notify(this.status(), "info");
  }

  status(): string {
    if (this.enabled() && !this.guardian) {
      return "auto mode is off: this session has no model catalog for the automatic reviewer.";
    }
    const mode = this.enabled()
      ? "auto mode is on: the automatic reviewer answers approvals, and asks you when it refuses."
      : "auto mode is off: you answer every approval yourself.";
    return `${mode}\n${this.context ? this.guardianStatus(this.context) : ""}`;
  }

  readonly guardianControl: GuardianControl = {
    snapshot: (ctx) => guardianSettings(this.guardianOverride ?? this.setup.config.guardian, this.guardianOverride !== undefined, ctx),
    change: async (command, ctx) => {
      this.context = ctx;
      if (command === "reset") { this.guardianOverride = undefined; return; }
      const [kind, ...values] = command.trim().split(/\s+/);
      let selection = { ...(this.guardianOverride ?? this.setup.config.guardian) };
      if (kind === "model" && values.length === 1 && values[0] === "session") selection = { effort: selection.effort };
      else if (kind === "model" && values.length === 2) selection = { ...selection, provider: values[0], model: values[1] };
      else if (kind === "effort" && values.length === 1) selection.effort = values[0] === "default" ? undefined : values[0];
      else throw new Error("Use /security guardian model <provider> <model>, model session, effort <level|default>, reset or status.");
      selection = readGuardianSelection(selection);
      const { model } = resolveGuardian(selection, ctx);
      const auth = await ctx.modelRegistry.getApiKeyAndHeaders(model);
      if (!auth.ok) throw new Error(`Guardian ${model.provider}/${model.id}: ${auth.error}`);
      this.guardianOverride = selection;
    },
  };

  guardianStatus(ctx: ExtensionContext): string {
    const settings = this.guardianControl.snapshot(ctx);
    return `Guardian (${settings.source}): ${settings.problem ?? settings.effective}. Overrides last until the agent reloads.`;
  }

  private async permissionsCommand(args: string, ctx: ExtensionContext): Promise<void> {
    const asked = args.trim().toLowerCase();
    if (asked === "" && ctx.mode === "tui") {
      const picked = await pickPreset(ctx, presetFor(this.pair.approvalPolicy, this.pair.sandboxMode)?.id);
      if (picked) await this.adopt(picked, ctx);
      return;
    }
    if (asked === "" || asked === "status") {
      ctx.ui?.notify(this.permissionsStatus(), "info");
      return;
    }
    const preset = presetByID(asked);
    if (!preset) {
      ctx.ui?.notify(
        `unknown permissions preset ${JSON.stringify(asked)}; use read-only, default, full-access, untrusted or status`,
        "error",
      );
      return;
    }
    await this.adopt(preset, ctx);
  }

  private async adopt(preset: Preset, ctx: ExtensionContext): Promise<void> {
    if (preset.id === "full-access" && !(await ctx.ui?.confirm("Enable full access?", preset.description))) {
      ctx.ui?.notify(`Permissions unchanged: ${this.describe()}.`, "info");
      return;
    }
    try {
      await this.setPermissions(preset);
    } catch (error) {
      ctx.ui?.notify(
        `Permissions: ${this.describe()} for this session, but this session's file tools did not rebuild: ` +
          `${message(error)}. Run /permissions again.`,
        "error",
      );
      return;
    }
    ctx.ui?.notify(`Permissions: ${this.describe()} for this session`, "info");
  }

  permissionsStatus(): string {
    return `Permissions: ${this.describe()}. Set at launch by attn; /permissions changes this session only, ` +
      `and a relaunch returns to the launch choice.`;
  }

  private describe(): string {
    const preset = presetFor(this.pair.approvalPolicy, this.pair.sandboxMode);
    return preset
      ? `${preset.id} (${preset.approvalPolicy}, ${preset.sandboxMode})`
      : `${describePermissions(this.pair.approvalPolicy, this.pair.sandboxMode)} (no preset)`;
  }

  private paint(ctx: ExtensionContext): void {
    const permissions = describePermissions(this.pair.approvalPolicy, this.pair.sandboxMode);
    ctx.ui?.setStatus(statusKey, `auto: ${this.enabled() && this.guardian ? "on" : "off"} · ${permissions}`);
  }

  private speak(ctx: ExtensionContext): void {
    if (this.noticed || !ctx.ui) return;
    this.noticed = true;
    if (this.setup.notice !== undefined) ctx.ui.notify(this.setup.notice, "warning");
    for (const problem of this.problems) ctx.ui.notify(problem, "warning");
    if (this.setup.config.network.enabled && !this.setup.proxy) {
      ctx.ui.notify(
        "attn configured network policy for this session but its proxy is not running, so sandboxed commands run offline. " +
          "A command that needs the network can ask for sandbox_permissions=require_escalated and runs outside the sandbox after review.",
        "warning",
      );
    }
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
