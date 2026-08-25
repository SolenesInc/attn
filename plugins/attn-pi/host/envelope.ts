
export const SEMANTIC_KINDS = [
  "session_ready",
  "run_started",
  "run_settled",
  "input_taken",
  "tool_started",
  "tool_finished",
  "model_changed",
] as const;

export const RENDER_KINDS = [
  "message_start",
  "message_delta",
  "message_end",
  "queue_update",
  "tool_detail",
  "conversation_snapshot",
  "conversation_page",
  "notice",
] as const;

/** The rest of the semantic family are facts about a run already open: re-declaring
 * `working` on every tool boundary would restamp `state_since` many times a minute. */
export const STATE_DECLARATION_KINDS = ["session_ready", "run_started", "run_settled"] as const;

export type SemanticKind = (typeof SEMANTIC_KINDS)[number];
export type RenderKind = (typeof RENDER_KINDS)[number];
export type EnvelopeKind = SemanticKind | RenderKind;

/** `idle` and `waiting_input` differ only in telling the user WHY the session went
 * quiet: a run that ended on its own, versus one stopped mid-exchange. */
export type HostSessionState = "working" | "idle" | "waiting_input";

export interface Envelope {
  session_id: string;
  seq: number;
  kind: EnvelopeKind;
  body: unknown;
}

export interface DeclarationBody {
  state: HostSessionState;
}

export interface SessionReadyBody extends DeclarationBody {
  session_file: string | null;
  model: string;
  cwd: string;
  pi_version: string;
  models: string[];
}

/** Semantic rather than a rendering: attn stores the model in the launch intent, so a
 * mid-session switch must reach the daemon. NOT a state declaration. */
export interface ModelChangedBody {
  model: string;
  error?: string;
}

export interface NoticeBody {
  id: string;
  level: NoticeLevel;
  text: string;
  done: boolean;
}

export type NoticeLevel = "info" | "warn" | "error";

export interface RunStartedBody extends DeclarationBody {}

export interface RunSettledBody extends DeclarationBody {
  error?: string;
}

export interface InputTakenBody {
  input_id: string;
}

export interface MessageStartBody {
  id: string;
  role: string;
}

export interface MessageDeltaBody {
  id: string;
  text: string;
}

export interface MessageEndBody {
  id: string;
  role: string;
  text: string;
}

export interface QueueUpdateBody {
  steering: string[];
  followUp: string[];
}

/** Deliberately small — the transcript's permanent record of the call. Inlining tool
 * output is how the corpus got a p99 11.6 MB transcript with ~0.4% message text. */
export interface ToolStartedBody {
  call_id: string;
  name: string;
  summary: string;
  files: string[];
}

export interface ToolFinishedBody {
  call_id: string;
  name: string;
  status: "ok" | "error";
  summary: string;
  files: string[];
  detail: boolean;
  patch: boolean;
  truncated: boolean;
  full_output: boolean;
  error?: string;
}

export interface ToolDetailBody {
  call_id: string;
  text: string;
  patch?: string;
  full: boolean;
  truncated: boolean;
  full_output_path?: string;
  error?: string;
}

type PiEvent = { type: string; [key: string]: unknown };

/** pi's own tool-output cap is 2,000 lines / 50 KB; 2,000 characters is 4% of that,
 * past any command line or error headline, and the whole text is one expand away. */
export const SUMMARY_LIMIT = 2000;

export function clipSummary(text: string, limit = SUMMARY_LIMIT): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  if (collapsed.length <= limit) return collapsed;
  return `${collapsed.slice(0, limit)}… [clipped at ${limit} of ${collapsed.length} characters]`;
}

function argString(args: unknown, key: string): string {
  if (!args || typeof args !== "object") return "";
  const value = (args as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

export function toolSummary(name: string, args: unknown): string {
  switch (name) {
    case "bash":
      return clipSummary(argString(args, "command"));
    case "read":
    case "write":
    case "edit":
      return clipSummary(argString(args, "path"));
    case "ls":
      return clipSummary(argString(args, "path") || ".");
    case "grep":
    case "find": {
      const pattern = argString(args, "pattern");
      const path = argString(args, "path");
      return clipSummary(path ? `${pattern} in ${path}` : pattern);
    }
    default: {
      if (!args || typeof args !== "object") return "";
      for (const value of Object.values(args as Record<string, unknown>)) {
        if (typeof value === "string" && value.trim() !== "") return clipSummary(value);
      }
      return "";
    }
  }
}

export function toolFiles(name: string, args: unknown): string[] {
  switch (name) {
    case "read":
    case "write":
    case "edit": {
      const path = argString(args, "path");
      return path ? [path] : [];
    }
    default:
      return [];
  }
}

export interface ToolDetail {
  text: string;
  patch?: string;
  truncated: boolean;
  fullOutputPath?: string;
}

export class ToolDetailStore {
  private entries = new Map<string, ToolDetail>();
  private bytes = 0;
  private evicted = 0;

  constructor(private readonly budgetBytes: number) {}

  put(callId: string, detail: ToolDetail): void {
    this.remove(callId);
    const size = detailBytes(detail);
    // One detail larger than the whole budget would evict everything and then itself;
    // keep it rather than the history, since it is the one about to be expanded.
    this.entries.set(callId, detail);
    this.bytes += size;
    for (const [key, held] of this.entries) {
      if (this.bytes <= this.budgetBytes || key === callId) break;
      this.entries.delete(key);
      this.bytes -= detailBytes(held);
      this.evicted += 1;
    }
  }

  get(callId: string): ToolDetail | undefined {
    return this.entries.get(callId);
  }

  missingReason(callId: string): string {
    return (
      `no detail held for tool call ${callId}: this host keeps the most recent ` +
      `${(this.budgetBytes / (1 << 20)).toFixed(0)} MB of tool output and has dropped ${this.evicted} older call(s)`
    );
  }

  private remove(callId: string): void {
    const existing = this.entries.get(callId);
    if (!existing) return;
    this.entries.delete(callId);
    this.bytes -= detailBytes(existing);
  }

  get retainedBytes(): number {
    return this.bytes;
  }

  get size(): number {
    return this.entries.size;
  }
}

function detailBytes(detail: ToolDetail): number {
  return detail.text.length + (detail.patch?.length ?? 0);
}

export function toolResultText(result: unknown): string {
  if (!result || typeof result !== "object") return "";
  const content = (result as { content?: unknown }).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  const parts: string[] = [];
  for (const block of content) {
    if (block && typeof block === "object" && (block as { type?: unknown }).type === "text") {
      const text = (block as { text?: unknown }).text;
      if (typeof text === "string") parts.push(text);
    }
  }
  return parts.join("\n");
}

export interface EnvelopeSink {
  (envelope: Envelope): void;
}

export class EnvelopeStream {
  private seq = 0;

  constructor(
    private readonly sessionID: string,
    private readonly sink: EnvelopeSink,
  ) {}

  emit(kind: EnvelopeKind, body: unknown): Envelope {
    this.seq += 1;
    const envelope: Envelope = { session_id: this.sessionID, seq: this.seq, kind, body };
    this.sink(envelope);
    return envelope;
  }
}

/** Receipt: a thinking model bursts to 1,970 `message_update` events/s (2026-08-04,
 * s2-delta-rate) and WS clients buffer 256. A 30 ms window caps one session at ~33/s. */
export class DeltaCoalescer {
  private pending = new Map<string, string>();
  private timer: unknown = null;

  constructor(
    private readonly windowMs: number,
    private readonly emit: (messageID: string, text: string) => void,
    private readonly schedule: (fn: () => void, ms: number) => unknown = setTimeout,
    private readonly cancel: (handle: unknown) => void = (handle) => clearTimeout(handle as never),
  ) {}

  push(messageID: string, text: string): void {
    if (text === "") return;
    this.pending.set(messageID, (this.pending.get(messageID) ?? "") + text);
    if (this.timer === null) {
      this.timer = this.schedule(() => {
        this.timer = null;
        this.flush();
      }, this.windowMs);
    }
  }

  flush(): void {
    if (this.timer !== null) {
      this.cancel(this.timer);
      this.timer = null;
    }
    if (this.pending.size === 0) return;
    const batch = this.pending;
    this.pending = new Map();
    for (const [messageID, text] of batch) {
      this.emit(messageID, text);
    }
  }
}

/** pi does not raise on a provider error: it persists `stopReason: "error"` with the
 * provider's response, routinely JSON wrapping JSON, so this digs for `error.message`. */
export function messageFailure(message: unknown): string {
  if (!message || typeof message !== "object") return "";
  if ((message as { stopReason?: unknown }).stopReason !== "error") return "";
  const raw = readString(message, "errorMessage");
  if (raw === "") return "The provider reported an error with no message.";
  let best = raw;
  let current: unknown = raw;
  for (let depth = 0; depth < 8; depth += 1) {
    if (typeof current !== "string") break;
    let parsed: unknown;
    try {
      parsed = JSON.parse(current);
    } catch {
      break;
    }
    const error = parsed && typeof parsed === "object" ? (parsed as { error?: unknown }).error : undefined;
    const inner = readString(error, "message");
    if (inner === "") break;
    best = inner;
    current = inner;
  }
  return best.replace(/\s+/g, " ").trim();
}

export function messageText(message: unknown): string {
  if (typeof message === "string") return message;
  if (message === null || typeof message !== "object") return "";
  const content = (message as { content?: unknown }).content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  let text = "";
  for (const block of content) {
    if (block && typeof block === "object" && (block as { type?: unknown }).type === "text") {
      const value = (block as { text?: unknown }).text;
      if (typeof value === "string") text += value;
    }
  }
  return text;
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : [];
}

function numberOr(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function readString(source: unknown, key: string): string {
  if (!source || typeof source !== "object") return "";
  const value = (source as Record<string, unknown>)[key];
  return typeof value === "string" ? value : "";
}

function noticeLevel(value: string): NoticeLevel {
  return value === "warn" || value === "error" ? value : "info";
}

/** `toolResult` is a message whose whole body is one tool's output; drawing it inlines
 * every byte the card keeps out (`seq 1 5000` measured 23,893 bytes, 2026-08-06). */
const UNRENDERED_ROLES = new Set(["toolResult"]);

function messageRole(message: unknown): string {
  if (message && typeof message === "object") {
    const role = (message as { role?: unknown }).role;
    if (typeof role === "string" && role !== "") return role;
  }
  return "assistant";
}

/** pi grows its event union without labelling it (0.80.10 -> 0.83.0 added four types
 * unannounced), so this switch has a default arm. Only attn's own kinds are closed. */
export class PiEventMapper {
  private messageCounter = 0;
  private currentMessageID: string | null = null;
  private readonly seenUnknown = new Set<string>();
  /** `tool_execution_end` carries the result but not the arguments, so the call has to
   * be remembered from its start. Bounded by concurrency, not by session length. */
  private readonly openCalls = new Map<string, { name: string; summary: string; files: string[] }>();
  private currentRole = "assistant";
  private noticeCounter = 0;
  private pendingInputs: Array<{ inputID: string; text: string }> = [];
  private readonly openNotices = new Map<string, string>();

  constructor(
    private readonly stream: EnvelopeStream,
    private readonly deltas: DeltaCoalescer,
    private readonly onUnknown: (type: string) => void = () => {},
    private readonly details: ToolDetailStore | null = null,
  ) {}

  handle(event: PiEvent): void {
    switch (event.type) {
      case "agent_start": {
        this.deltas.flush();
        const body: RunStartedBody = { state: "working" };
        this.stream.emit("run_started", body);
        return;
      }

      case "agent_settled": {
        this.deltas.flush();
        const body: RunSettledBody = { state: "idle" };
        this.stream.emit("run_settled", body);
        return;
      }

      case "queue_update": {
        // Ahead of the text it is about: a drain fires immediately before the user
        // message it delivered, so the app draws the queue emptying, then the message.
        this.deltas.flush();
        const body: QueueUpdateBody = {
          steering: stringList(event.steering),
          followUp: stringList(event.followUp),
        };
        this.stream.emit("queue_update", body);
        return;
      }

      case "tool_execution_start": {
        // Ahead of any text still pending: the card belongs after the sentence
        // that announced it, not before.
        this.deltas.flush();
        const callID = typeof event.toolCallId === "string" ? event.toolCallId : "";
        const name = typeof event.toolName === "string" ? event.toolName : "";
        if (callID === "") return;
        const call = { name, summary: toolSummary(name, event.args), files: toolFiles(name, event.args) };
        this.openCalls.set(callID, call);
        const body: ToolStartedBody = { call_id: callID, ...call };
        this.stream.emit("tool_started", body);
        return;
      }

      case "tool_execution_update":
        // The bash tool's throttled partial output, deliberately dropped: the finished
        // card carries the same text one expand away. Named so it is not read as unknown.
        return;

      case "tool_execution_end": {
        this.deltas.flush();
        const callID = typeof event.toolCallId === "string" ? event.toolCallId : "";
        if (callID === "") return;
        const name = typeof event.toolName === "string" ? event.toolName : "";
        const started = this.openCalls.get(callID);
        this.openCalls.delete(callID);
        const isError = event.isError === true;
        const result = event.result;
        const text = toolResultText(result);
        const toolDetails = (result as { details?: unknown } | undefined)?.details;
        const patch = readString(toolDetails, "patch");
        const fullOutputPath = readString(toolDetails, "fullOutputPath");
        const truncation = toolDetails && typeof toolDetails === "object"
          ? (toolDetails as { truncation?: unknown }).truncation
          : undefined;
        const truncated = truncation !== null && typeof truncation === "object"
          && (truncation as { truncated?: unknown }).truncated === true;

        if (this.details && (text !== "" || patch !== "")) {
          this.details.put(callID, {
            text,
            patch: patch === "" ? undefined : patch,
            truncated,
            fullOutputPath: fullOutputPath === "" ? undefined : fullOutputPath,
          });
        }

        const body: ToolFinishedBody = {
          call_id: callID,
          name: started?.name || name,
          status: isError ? "error" : "ok",
          summary: started?.summary ?? "",
          files: started?.files ?? [],
          detail: text !== "" || patch !== "",
          patch: patch !== "",
          truncated,
          full_output: fullOutputPath !== "",
        };
        if (isError && text !== "") body.error = clipSummary(text);
        this.stream.emit("tool_finished", body);
        return;
      }

      case "compaction_start": {
        this.openNotice("compaction", "info", `Compacting the conversation (${readString(event, "reason") || "threshold"})...`);
        return;
      }

      case "compaction_end": {
        const failure = readString(event, "errorMessage");
        const aborted = event.aborted === true;
        const result = event.result as { tokensBefore?: unknown } | undefined;
        const before = typeof result?.tokensBefore === "number" ? result.tokensBefore : 0;
        if (aborted) this.closeNotice("compaction", "warn", "Compaction was cancelled");
        else if (failure !== "") this.closeNotice("compaction", "error", `Compaction failed: ${clipSummary(failure)}`);
        else if (before > 0) this.closeNotice("compaction", "info", `Compacted the conversation (${before.toLocaleString("en-US")} tokens summarized)`);
        else this.closeNotice("compaction", "info", "Compacted the conversation");
        return;
      }

      case "auto_retry_start": {
        const attempt = numberOr(event.attempt, 1);
        const max = numberOr(event.maxAttempts, 0);
        const delay = numberOr(event.delayMs, 0);
        const of = max > 0 ? ` ${attempt}/${max}` : ` ${attempt}`;
        const wait = delay > 0 ? ` in ${Math.round(delay / 1000)}s` : "";
        this.openNotice("retry", "warn", `Retrying${of}${wait}: ${clipSummary(readString(event, "errorMessage"))}`);
        return;
      }

      case "auto_retry_end": {
        if (event.success === true) {
          this.closeNotice("retry", "info", `Recovered after ${numberOr(event.attempt, 1)} retry attempt(s)`);
          return;
        }
        const failure = readString(event, "finalError");
        this.closeNotice("retry", "error", `Gave up after ${numberOr(event.attempt, 1)} retry attempt(s)${failure === "" ? "" : `: ${clipSummary(failure)}`}`);
        return;
      }

      case "summarization_retry_scheduled": {
        const attempt = numberOr(event.attempt, 1);
        const max = numberOr(event.maxAttempts, 0);
        const of = max > 0 ? ` ${attempt}/${max}` : ` ${attempt}`;
        this.openNotice("summarization", "warn", `Summarization failed; retrying${of}: ${clipSummary(readString(event, "errorMessage"))}`);
        return;
      }

      case "summarization_retry_attempt_start":
        return;

      case "summarization_retry_finished": {
        this.closeNotice("summarization", "info", "Summarization retry finished");
        return;
      }

      case "message_start": {
        const role = messageRole(event.message);
        if (role === "user") this.takeInput(messageText(event.message));
        // Nothing is emitted here: pi opens a message before anyone knows whether it
        // will say anything, and a tool RESULT arrives as a message of its own.
        this.deltas.flush();
        this.currentMessageID = null;
        this.currentRole = role;
        return;
      }

      case "message_update": {
        const inner = event.assistantMessageEvent as { type?: string; delta?: unknown } | undefined;
        if (!inner || inner.type !== "text_delta" || typeof inner.delta !== "string") return;
        if (UNRENDERED_ROLES.has(this.currentRole)) return;
        this.deltas.push(this.requireMessageID(), inner.delta);
        return;
      }

      case "message_end": {
        this.deltas.flush();
        const role = messageRole(event.message);
        const text = messageText(event.message);
        const open = this.currentMessageID;
        this.currentMessageID = null;
        this.currentRole = "assistant";
        // pi does not raise — it persists the message with `stopReason: "error"` — so
        // without this a retired-model 404 (2026-08-09) reaches the pane as silence.
        const failure = messageFailure(event.message);
        if (failure !== "") this.closeNotice("run", "error", `The agent could not answer: ${clipSummary(failure)}`);
        if (UNRENDERED_ROLES.has(role) || (open === null && text === "")) return;
        const id = open ?? this.mintMessage(role);
        this.stream.emit("message_end", { id, role, text } satisfies MessageEndBody);
        return;
      }

      default:
        if (!this.seenUnknown.has(event.type)) {
          this.seenUnknown.add(event.type);
          this.onUnknown(event.type);
        }
    }
  }

  expectInput(inputID: string, text: string): void {
    if (inputID.trim() === "") return;
    this.pendingInputs.push({ inputID, text });
  }

  forgetInput(inputID: string): void {
    this.pendingInputs = this.pendingInputs.filter((candidate) => candidate.inputID !== inputID);
  }

  private takeInput(text: string): void {
    const index = this.pendingInputs.findIndex((candidate) => candidate.text === text);
    if (index < 0) return;
    const [candidate] = this.pendingInputs.splice(index, 1);
    this.stream.emit("input_taken", { input_id: candidate.inputID } satisfies InputTakenBody);
  }

  private requireMessageID(): string {
    if (this.currentMessageID === null) this.currentMessageID = this.mintMessage(this.currentRole);
    return this.currentMessageID;
  }

  private openNotice(concern: string, level: NoticeLevel, text: string): void {
    this.deltas.flush();
    this.noticeCounter += 1;
    const id = `n${this.noticeCounter}`;
    this.openNotices.set(concern, id);
    this.stream.emit("notice", { id, level, text, done: false } satisfies NoticeBody);
  }

  private closeNotice(concern: string, level: NoticeLevel, text: string): void {
    this.deltas.flush();
    let id = this.openNotices.get(concern);
    if (id === undefined) {
      this.noticeCounter += 1;
      id = `n${this.noticeCounter}`;
    }
    this.openNotices.delete(concern);
    this.stream.emit("notice", { id, level, text, done: true } satisfies NoticeBody);
  }

  private mintMessage(role: string): string {
    this.messageCounter += 1;
    const id = `m${this.messageCounter}`;
    this.stream.emit("message_start", { id, role } satisfies MessageStartBody);
    return id;
  }
}

export interface SnapshotMessageItem {
  kind: "message";
  id: string;
  role: string;
  text: string;
  streaming: boolean;
}

export interface SnapshotToolItem {
  kind: "tool";
  call_id: string;
  name: string;
  summary: string;
  files: string[];
  status: "running" | "ok" | "error";
  error?: string;
  detail: boolean;
  patch: boolean;
  truncated: boolean;
  full_output: boolean;
}

export interface SnapshotNoticeItem {
  kind: "notice";
  id: string;
  level: NoticeLevel;
  text: string;
  done: boolean;
}

export type SnapshotItem = SnapshotMessageItem | SnapshotToolItem | SnapshotNoticeItem;

/** The cursor must survive an item being replaced in place, so it is the item's own
 * identity. Twin of `conversationItemKey` in app/src/store/conversations.ts. */
export function snapshotItemKey(item: SnapshotItem): string {
  return item.kind === "tool" ? `tool:${item.call_id}` : `${item.kind}:${item.id}`;
}

/** `epoch` names the host process that built it: a replacement mints its own item ids,
 * so same-epoch answers merge and a new epoch replaces. */
export interface ConversationSnapshotBody {
  epoch: string;
  items: SnapshotItem[];
  total: number;
  truncated: boolean;
  has_more: boolean;
  dropped: number;
  running: boolean;
  queue: QueueUpdateBody;
}

export interface ConversationPageBody {
  epoch: string;
  before: string;
  items: SnapshotItem[];
  has_more: boolean;
}

/** A render budget before a wire budget: every item is a DOM node. 500 is past any
 * conversation still readable by scrolling; longer ones are slice 5's paging. */
export const SNAPSHOT_ITEM_LIMIT = 500;

/** The corpus puts ~46 KB of message text in a p99 transcript; 1 MB is ~20x that and
 * three orders under the daemon's 64 MB envelope-line ceiling. Tool output is not here. */
export const SNAPSHOT_BYTES_LIMIT = 1 << 20;

/** Receipt (2026-08-09, 585 real conversations off disk): p50 5 items, p99 5,442,
 * longest ever 11,305. 50,000 is 4.4x that longest one. */
export const TRANSCRIPT_RETENTION_ITEMS = 50_000;

/** Same corpus, message TEXT: p50 1.3 KB, p99 1.0 MB, largest conversation 1.7 MB.
 * 32 MB is ~19x that largest one, and a quarter of the host's 130 MB idle RSS. */
export const TRANSCRIPT_RETENTION_BYTES = 32 << 20;

export function retentionBudget(
  name: string,
  raw: string,
  fallback: number,
  warn: (message: string) => void,
): number {
  if (raw === "") return fallback;
  const value = Number(raw);
  if (!Number.isFinite(value) || !Number.isInteger(value) || value < 1) {
    warn(`${name}=${raw} is not a positive whole number; using ${fallback}`);
    return fallback;
  }
  return value;
}

function itemBytes(item: SnapshotItem): number {
  if (item.kind === "message") return item.text.length + item.role.length + item.id.length;
  if (item.kind === "notice") return item.text.length + item.id.length;
  return item.summary.length + item.name.length + (item.error?.length ?? 0)
    + item.files.reduce((total, file) => total + file.length, 0);
}

const isSnapshotTool = (callID: string) => (item: SnapshotItem): item is SnapshotToolItem =>
  item.kind === "tool" && item.call_id === callID;
const isSnapshotMessage = (id: string) => (item: SnapshotItem): item is SnapshotMessageItem =>
  item.kind === "message" && item.id === id;
const isSnapshotNotice = (id: string) => (item: SnapshotItem): item is SnapshotNoticeItem =>
  item.kind === "notice" && item.id === id;

export class TranscriptStore {
  private items: SnapshotItem[] = [];
  private bytes = 0;
  private dropped = 0;
  private running = false;
  private queue: QueueUpdateBody = { steering: [], followUp: [] };

  constructor(
    private readonly epoch: string = "",
    private readonly windowItems: number = SNAPSHOT_ITEM_LIMIT,
    private readonly windowBytes: number = SNAPSHOT_BYTES_LIMIT,
    private readonly retentionItems: number = TRANSCRIPT_RETENTION_ITEMS,
    private readonly retentionBytes: number = TRANSCRIPT_RETENTION_BYTES,
  ) {}

  seed(items: SnapshotItem[]): void {
    this.items = [];
    this.bytes = 0;
    this.dropped = 0;
    for (const item of items) this.push(item);
  }

  apply(kind: EnvelopeKind, body: unknown): void {
    const fields = (body ?? {}) as Record<string, unknown>;
    switch (kind) {
      case "run_started":
        this.running = true;
        return;
      case "run_settled": {
        this.running = false;
        // Whatever was open when the run closed is closed: the host emits message_end
        // before the settle, so a message still open here ended under the run.
        for (const item of this.items) {
          if (item.kind === "message") item.streaming = false;
          else if (item.kind === "tool" && item.status === "running") {
            item.status = "error";
            item.error = "the run ended before this tool reported";
          }
        }
        return;
      }
      case "queue_update":
        this.queue = { steering: stringList(fields.steering), followUp: stringList(fields.followUp) };
        return;
      case "message_start": {
        const id = readString(fields, "id");
        if (id === "" || this.items.some(isSnapshotMessage(id))) return;
        this.push({ kind: "message", id, role: readString(fields, "role") || "assistant", text: "", streaming: true });
        return;
      }
      case "message_delta": {
        const id = readString(fields, "id");
        const delta = readString(fields, "text");
        if (id === "" || delta === "") return;
        const open = this.items.find(isSnapshotMessage(id));
        if (!open) {
          this.push({ kind: "message", id, role: "assistant", text: delta, streaming: true });
          return;
        }
        open.text += delta;
        this.bytes += delta.length;
        this.trim();
        return;
      }
      case "message_end": {
        const id = readString(fields, "id");
        if (id === "") return;
        const settled: SnapshotMessageItem = {
          kind: "message",
          id,
          role: readString(fields, "role") || "assistant",
          text: readString(fields, "text"),
          streaming: false,
        };
        this.replaceOrPush(isSnapshotMessage(id), settled);
        return;
      }
      case "tool_started": {
        const callID = readString(fields, "call_id");
        if (callID === "" || this.items.some(isSnapshotTool(callID))) return;
        this.push({
          kind: "tool",
          call_id: callID,
          name: readString(fields, "name"),
          summary: readString(fields, "summary"),
          files: stringList(fields.files),
          status: "running",
          detail: false,
          patch: false,
          truncated: false,
          full_output: false,
        });
        return;
      }
      case "notice": {
        const id = readString(fields, "id");
        if (id === "") return;
        const notice: SnapshotNoticeItem = {
          kind: "notice",
          id,
          level: noticeLevel(readString(fields, "level")),
          text: readString(fields, "text"),
          done: fields.done === true,
        };
        this.replaceOrPush(isSnapshotNotice(id), notice);
        return;
      }
      case "tool_finished": {
        const callID = readString(fields, "call_id");
        if (callID === "") return;
        const existing = this.items.find(isSnapshotTool(callID));
        const error = readString(fields, "error");
        const finished: SnapshotToolItem = {
          kind: "tool",
          call_id: callID,
          name: readString(fields, "name") || existing?.name || "",
          summary: readString(fields, "summary") || existing?.summary || "",
          files: stringList(fields.files).length > 0 ? stringList(fields.files) : existing?.files ?? [],
          status: readString(fields, "status") === "error" ? "error" : "ok",
          ...(error === "" ? {} : { error }),
          detail: fields.detail === true,
          patch: fields.patch === true,
          truncated: fields.truncated === true,
          full_output: fields.full_output === true,
        };
        this.replaceOrPush(isSnapshotTool(callID), finished);
        return;
      }
      default:
        return;
    }
  }

  snapshot(): ConversationSnapshotBody {
    const window = this.window(this.items.length);
    return {
      epoch: this.epoch,
      items: window,
      total: this.items.length + this.dropped,
      truncated: window.length < this.items.length + this.dropped,
      has_more: window.length < this.items.length,
      dropped: this.dropped,
      running: this.running,
      queue: { steering: [...this.queue.steering], followUp: [...this.queue.followUp] },
    };
  }

  page(before: string): ConversationPageBody {
    const end = this.items.findIndex((item) => snapshotItemKey(item) === before);
    const items = end <= 0 ? [] : this.window(end);
    return {
      epoch: this.epoch,
      before,
      items,
      has_more: end > items.length,
    };
  }

  private window(end: number): SnapshotItem[] {
    const window: SnapshotItem[] = [];
    let bytes = 0;
    for (let index = end - 1; index >= 0; index -= 1) {
      const item = this.items[index]!;
      // The newest item always travels, whatever it costs: a window that
      // refused it would hide the sentence the agent just wrote.
      if (window.length > 0 && (window.length >= this.windowItems || bytes + itemBytes(item) > this.windowBytes)) break;
      window.unshift({ ...item });
      bytes += itemBytes(item);
    }
    return window;
  }

  get size(): number {
    return this.items.length;
  }

  get retainedBytes(): number {
    return this.bytes;
  }

  private push(item: SnapshotItem): void {
    this.items.push(item);
    this.bytes += itemBytes(item);
    this.trim();
  }

  private replaceOrPush(match: (item: SnapshotItem) => boolean, replacement: SnapshotItem): void {
    const index = this.items.findIndex(match);
    if (index < 0) {
      this.push(replacement);
      return;
    }
    this.bytes -= itemBytes(this.items[index]!);
    this.items[index] = replacement;
    this.bytes += itemBytes(replacement);
    this.trim();
  }

  /** Never drops a message still being written: it stops being newest once pi opens a
   * tool beneath it, and evicting it makes the next delta mint a truncated one. */
  private trim(): void {
    while (this.items.length > 1 && (this.items.length > this.retentionItems || this.bytes > this.retentionBytes)) {
      const oldest = this.items[0]!;
      if (oldest.kind === "message" && oldest.streaming) return;
      this.items.shift();
      this.bytes -= itemBytes(oldest);
      this.dropped += 1;
    }
  }

  get droppedItems(): number {
    return this.dropped;
  }
}

export interface SessionEntryLike {
  type: string;
  id: string;
  message?: unknown;
  tokensBefore?: unknown;
  provider?: unknown;
  modelId?: unknown;
}

export interface ReconstructedTranscript {
  items: SnapshotItem[];
  details: Map<string, ToolDetail>;
}

function contentBlocks(message: unknown): unknown[] {
  if (!message || typeof message !== "object") return [];
  const content = (message as { content?: unknown }).content;
  return Array.isArray(content) ? content : [];
}

/** Message ids are namespaced `h:` after the entry that produced them, so a revived
 * host minting `m1` cannot collide with a message that came off disk. */
export function reconstructTranscript(entries: SessionEntryLike[]): ReconstructedTranscript {
  const items: SnapshotItem[] = [];
  const details = new Map<string, ToolDetail>();
  const toolsByCallID = new Map<string, SnapshotToolItem>();

  for (const entry of entries) {
    if (entry.type === "compaction") {
      const before = typeof entry.tokensBefore === "number" ? entry.tokensBefore : 0;
      items.push({
        kind: "notice",
        id: `h:${entry.id}`,
        level: "info",
        text: before > 0
          ? `Compacted the conversation (${before.toLocaleString("en-US")} tokens summarized)`
          : "Compacted the conversation",
        done: true,
      });
      continue;
    }
    if (entry.type === "model_change") {
      // pi writes a model_change into every session before anything is said — what it
      // opened on, not a switch (measured on a fresh conversation, pi 0.83.0).
      if (!items.some((item) => item.kind === "message")) continue;
      const provider = typeof entry.provider === "string" ? entry.provider : "";
      const modelID = typeof entry.modelId === "string" ? entry.modelId : "";
      if (provider !== "" && modelID !== "") {
        items.push({ kind: "notice", id: `h:${entry.id}`, level: "info", text: `Model switched to ${provider}/${modelID}`, done: true });
      }
      continue;
    }
    if (entry.type !== "message") continue;
    const message = entry.message;
    const role = messageRole(message);
    if (role === "toolResult") {
      const callID = readString(message, "toolCallId");
      const card = callID === "" ? undefined : toolsByCallID.get(callID);
      if (!card) continue;
      const result = message as { isError?: unknown; details?: unknown };
      const text = toolResultText(message);
      const resultDetails = result.details;
      const patch = readString(resultDetails, "patch");
      const fullOutputPath = readString(resultDetails, "fullOutputPath");
      const truncation = resultDetails && typeof resultDetails === "object"
        ? (resultDetails as { truncation?: unknown }).truncation
        : undefined;
      const truncated = truncation !== null && typeof truncation === "object"
        && (truncation as { truncated?: unknown }).truncated === true;
      card.status = result.isError === true ? "error" : "ok";
      if (result.isError === true && text !== "") card.error = clipSummary(text);
      card.detail = text !== "" || patch !== "";
      card.patch = patch !== "";
      card.truncated = truncated;
      card.full_output = fullOutputPath !== "";
      if (card.detail) {
        details.set(callID, {
          text,
          patch: patch === "" ? undefined : patch,
          truncated,
          fullOutputPath: fullOutputPath === "" ? undefined : fullOutputPath,
        });
      }
      continue;
    }
    const text = messageText(message);
    if (text !== "") {
      items.push({ kind: "message", id: `h:${entry.id}`, role, text, streaming: false });
    }
    const failure = messageFailure(message);
    if (failure !== "") {
      items.push({
        kind: "notice",
        id: `h:${entry.id}:error`,
        level: "error",
        text: `The agent could not answer: ${clipSummary(failure)}`,
        done: true,
      });
    }
    for (const block of contentBlocks(message)) {
      if (!block || typeof block !== "object" || (block as { type?: unknown }).type !== "toolCall") continue;
      const callID = readString(block, "id");
      if (callID === "") continue;
      const name = readString(block, "name");
      const args = (block as { arguments?: unknown }).arguments;
      const card: SnapshotToolItem = {
        kind: "tool",
        call_id: callID,
        name,
        summary: toolSummary(name, args),
        files: toolFiles(name, args),
        // Every call starts running and is answered by its own toolResult entry below.
        // One that never gets an answer is what conversationInterrupted reads.
        status: "running",
        detail: false,
        patch: false,
        truncated: false,
        full_output: false,
      };
      toolsByCallID.set(callID, card);
      items.push(card);
    }
  }
  return { items, details };
}

export function conversationInterrupted(items: SnapshotItem[]): boolean {
  let index = items.length - 1;
  while (index >= 0 && items[index]!.kind === "notice") index -= 1;
  const last = items[index];
  if (!last) return false;
  return last.kind !== "message" || last.role !== "assistant";
}

/** Emptiness means nobody has SPOKEN: reconstruction mints notices for things that
 * happened TO a conversation, and counting those swallows a delegation's brief. */
export function launchPromptIsUndelivered(
  prompt: string,
  reopened: SnapshotItem[],
  forked: boolean,
): boolean {
  if (prompt.trim() === "") return false;
  if (forked) return true;
  return !reopened.some((item) => item.kind === "message");
}

export type HostVerbWithText = { verb: "prompt" | "steer" | "follow_up"; text: string; inputID?: string };
export type HostVerb =
  | HostVerbWithText
  | { verb: "shutdown" }
  | { verb: "clear_queue" }
  | { verb: "snapshot" }
  | { verb: "history"; before: string }
  | { verb: "set_model"; model: string }
  | { verb: "tool_detail"; callID: string; full: boolean };

const TEXT_VERBS = new Set(["prompt", "steer", "follow_up"]);

export function parseVerb(line: string): HostVerb {
  const value: unknown = JSON.parse(line);
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("verb must be a JSON object");
  }
  const verb = (value as { verb?: unknown }).verb;
  if (typeof verb === "string" && TEXT_VERBS.has(verb)) {
    const text = (value as { text?: unknown }).text;
    if (typeof text !== "string" || text.trim() === "") throw new Error(`${verb} verb needs non-empty text`);
    const inputID = (value as { input_id?: unknown }).input_id;
    if (inputID !== undefined && (typeof inputID !== "string" || inputID.trim() === "")) {
      throw new Error(`${verb} verb has an invalid input_id`);
    }
    return {
      verb: verb as HostVerbWithText["verb"],
      text,
      ...(typeof inputID === "string" ? { inputID: inputID.trim() } : {}),
    };
  }
  if (verb === "shutdown") return { verb: "shutdown" };
  if (verb === "clear_queue") return { verb: "clear_queue" };
  if (verb === "snapshot") return { verb: "snapshot" };
  if (verb === "history") {
    const before = (value as { before?: unknown }).before;
    if (typeof before !== "string" || before.trim() === "") throw new Error("history verb needs a before cursor");
    return { verb: "history", before };
  }
  if (verb === "set_model") {
    const model = (value as { model?: unknown }).model;
    if (typeof model !== "string" || model.trim() === "") throw new Error("set_model verb needs a model");
    return { verb: "set_model", model: model.trim() };
  }
  if (verb === "tool_detail") {
    const callID = (value as { call_id?: unknown }).call_id;
    if (typeof callID !== "string" || callID.trim() === "") throw new Error("tool_detail verb needs a call_id");
    return { verb: "tool_detail", callID, full: (value as { full?: unknown }).full === true };
  }
  throw new Error(`unsupported verb ${JSON.stringify(verb)}`);
}
