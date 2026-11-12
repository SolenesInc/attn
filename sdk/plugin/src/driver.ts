export type DriverEnvironment = Record<string, string>;

export type DriverLaunch = {
  argv: string[];
  env?: DriverEnvironment;
  cwd?: string;
};

export type DriverLaunchInstructions = {
  kind: string;
  content: string;
  profile_id?: string;
};

export type DriverSpawnParams = {
  agent: string;
  session_id: string;
  run_id: string;
  cwd: string;
  label?: string;
  yolo?: boolean;
  model?: string;
  effort?: string;
  initial_prompt?: string;
  metadata?: unknown;
  resume_session_id?: string;
  instructions?: DriverLaunchInstructions;
};

export type DriverVoice = "user" | "attn";

export type DriverDeliverParams = {
  session_id: string;
  run_id: string;
  input_id: string;
  text: string;
  voice?: DriverVoice;
  conversation_id?: string;
};

export type DriverCompactParams = {
  session_id: string;
  run_id: string;
  input_id: string;
  conversation_id: string;
  text: "";
  voice?: "attn";
};

export type DriverDeliverResult =
  | { ok: true }
  | { ok: false; status?: "deferred" | "rejected" | "target_ended"; reason?: string };

export type DriverResumeConversation = {
  agent: string;
  cwd: string;
  resume_session_id: string;
};

export type DriverResumeAvailability = DriverResumeConversation & {
  available: boolean;
  reason?: string;
};

export type DriverModel = Record<string, unknown>;

export type DriverModelCatalog = {
  models: DriverModel[];
  detail?: string;
};

export type DriverSessionClosedParams = {
  session_id: string;
  run_id: string;
  reason: string;
  exit_code?: number;
  signal?: string;
};

export type DriverHealth = { ok: boolean; message?: string };

export type DriverReportedState = "unknown" | "working" | "pending_approval" | "waiting_input" | "idle";
export type DriverStopVerdict = "idle" | "waiting_input" | "unknown";
export type DriverCompactionOutcome = "completed" | "busy" | "failed";

/** The calls a driver makes about one run, sharing the run's report sequence. */
export interface DriverRun {
  readonly terminalId: string;
  readonly runId: string;
  /** Metadata the driver last reported for this run; set after a reconnect. */
  readonly metadata: unknown;
  state(state: DriverReportedState, options?: { onlyIfUnknown?: boolean }): Promise<void>;
  /** Asks attn whether the final assistant text ends the turn or asks the user something. */
  classifyStop(assistantText: string): Promise<DriverStopVerdict>;
  stop(verdict: DriverStopVerdict): Promise<void>;
  /** `resumeSessionId` is the native conversation ID; attributed input and compaction wait for it. */
  reportMetadata(metadata: unknown, options?: { resumeSessionId?: string }): Promise<void>;
  transcriptPath(path: string): Promise<void>;
  pullRequest(url: string): Promise<void>;
  compaction(report: {
    inputId: string;
    conversationId: string;
    outcome: DriverCompactionOutcome;
    reason?: string;
  }): Promise<void>;
}

export type DriverHandlers = {
  /** Return the command attn runs in the terminal. Required. */
  spawn(params: DriverSpawnParams, run: DriverRun): DriverLaunch | Promise<DriverLaunch>;
  /** Declares `resume`. */
  resume?(params: DriverSpawnParams, run: DriverRun): DriverLaunch | Promise<DriverLaunch>;
  /** Declares `resume_availability`. Requires `resume`. */
  resumeAvailable?(
    conversations: DriverResumeConversation[],
  ): DriverResumeAvailability[] | Promise<DriverResumeAvailability[]>;
  /** Declares `model_discovery`. */
  models?(): DriverModelCatalog | Promise<DriverModelCatalog>;
  /**
   * Declares `message_delivery`: attn binds each run to this handler at launch.
   * Return `accepted()` only after taking custody, and answer a repeated `input_id`
   * with the same receipt.
   */
  deliver?(params: DriverDeliverParams, run: DriverRun): DriverDeliverResult | Promise<DriverDeliverResult>;
  /**
   * Declares `compaction`. Return `accepted()` for custody, then report the native
   * outcome with `run.compaction(...)`. Requires `deliver`.
   */
  compact?(params: DriverCompactParams, run: DriverRun): DriverDeliverResult | Promise<DriverDeliverResult>;
  sessionClosed?(params: DriverSessionClosedParams): void | Promise<void>;
  /** Called after every registration with the runs attn still holds for this plugin. */
  recover?(runs: DriverRun[]): void | Promise<void>;
  health?(): DriverHealth | Promise<DriverHealth>;
};

export type DriverPromises = {
  /** Each message carries `voice` and `conversation_id`. Requires `deliver`. */
  attributedInput?: boolean;
  /** Delivered text cannot answer a native permission question. Requires `deliver`. */
  safeDuringQuestion?: boolean;
  /** `spawn` and `resume` honor `yolo`. */
  yolo?: boolean;
  /** `spawn` honors `initial_prompt`; delegation needs it. */
  initialPrompt?: boolean;
  /** `spawn` and `resume` honor `model`. */
  modelPin?: boolean;
  /** `spawn` and `resume` honor `effort`. */
  effortPin?: boolean;
  /** `spawn` and `resume` honor `instructions`. */
  launchInstructions?: boolean;
  /** The driver reports every turn start and end with `run.state(...)`; attn stops inferring state from the terminal. */
  stateReporting?: boolean;
  /** With `launchInstructions`, attn does not ask the agent to report its pull requests itself. */
  pullRequestReporting?: boolean;
};

export type DriverDefinition = DriverHandlers & DriverPromises & {
  /** Stable harness ID: lowercase letters, numbers, hyphens and underscores, starting with a letter or number. */
  id: string;
};

export function accepted(): DriverDeliverResult {
  return { ok: true };
}

/** Custody was not taken and may be retried; the PTY is not used instead. */
export function deferred(reason = "Recipient unavailable"): DriverDeliverResult {
  return { ok: false, status: "deferred", reason };
}

export function rejected(reason: string): DriverDeliverResult {
  return { ok: false, status: "rejected", reason };
}

export function targetEnded(reason = "Target ended"): DriverDeliverResult {
  return { ok: false, status: "target_ended", reason };
}

const minimumDaemonBuild = "13595cac6922bb7b2d3d89d62e4879be500f7aa3";
const buildGatedCapabilities = ["attributed_input", "safe_during_question", "compaction"];

export function driverCapabilities(definition: DriverDefinition): Record<string, boolean> {
  const { deliver, resume, resumeAvailable, compact, models } = definition;
  if (typeof definition.spawn !== "function") {
    throw new Error("driver spawn handler is required");
  }
  if (resumeAvailable && !resume) {
    throw new Error("driver resumeAvailable requires resume");
  }
  const capabilities: Record<string, boolean> = {
    resume: !!resume,
    resume_availability: !!resumeAvailable,
    model_discovery: !!models,
    message_delivery: !!deliver,
    compaction: !!compact,
    attributed_input: !!definition.attributedInput,
    safe_during_question: !!definition.safeDuringQuestion,
    yolo: !!definition.yolo,
    initial_prompt: !!definition.initialPrompt,
    model_pin: !!definition.modelPin,
    effort_pin: !!definition.effortPin,
    launch_instructions: !!definition.launchInstructions,
    state_reporting: !!definition.stateReporting,
    pull_request_reporting: !!definition.pullRequestReporting,
  };
  return Object.fromEntries(Object.entries(capabilities).filter(([, enabled]) => enabled));
}

/** Turns the daemon's "unsupported driver capability" refusal into an upgrade instruction. */
export function explainRegistrationError(error: Error): Error {
  const unsupported = /unsupported driver capability "([a-z_]+)"/.exec(error.message)?.[1];
  if (!unsupported || !buildGatedCapabilities.includes(unsupported)) {
    return error;
  }
  return new Error(
    `this attn daemon does not support driver capability "${unsupported}"; upgrade attn to source build ${minimumDaemonBuild} or newer`,
  );
}
