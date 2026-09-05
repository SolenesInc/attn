import { actionEvidence, toolEvidenceLimits, type ActionEvidence, type ToolEvidenceLimits } from "./evidence";

export type TranscriptRole = "user" | "assistant" | "tool";
export type ToolObservation = "blocked" | "allowed-to-run" | "succeeded" | "failed" | "unknown";
export type ToolEvidence = {
  call: Omit<ActionEvidence, "input"> & { input?: Record<string, unknown> };
  observation: ToolObservation;
  omission?: { reason: string; originalBytes: number };
};

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;

  tool?: string;
  evidence?: ToolEvidence;
  omittedCalls?: number;
};

export class TranscriptWindow {
  private entries: TranscriptEntry[] = [];

  private opening: string | undefined;
  private sequence = 0;
  private omittedCalls = 0;
  private toolBytes = 0;
  private toolCount = 0;

  constructor(private readonly limits: ToolEvidenceLimits | (() => ToolEvidenceLimits) = toolEvidenceLimits()) {}

  record(role: TranscriptRole, text: string, tool?: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (role === "user") {
      if (this.opening === undefined && this.entries.every((entry) => entry.role === "tool")) {
        this.opening = trimmed;
        return;
      }
      if (trimmed === this.opening) return;
      if (this.latest("user") === trimmed) return;
    }
    this.entries.push({ role, text: trimmed, ...(tool === undefined ? {} : { tool }) });
  }

  recordAction(action: ActionEvidence, observation: ToolObservation): void {
    const limits = typeof this.limits === "function" ? this.limits() : this.limits;
    const call = actionEvidence(action, action.cwd);
    call.toolCallId ??= `unidentified-${++this.sequence}`;
    const originalBytes = Buffer.byteLength(JSON.stringify(call));
    const evidence: ToolEvidence = { call, observation };
    if (originalBytes > limits.entryBytes) {
      const { input: _input, ...identity } = call;
      evidence.call = identity;
      evidence.omission = { reason: "tool input exceeded history entry limit", originalBytes };
    }
    const entry: TranscriptEntry = { role: "tool", text: "", evidence };
    const previous = this.entries.findIndex((item) => item.evidence?.call.toolCallId === call.toolCallId);
    if (previous >= 0) {
      this.toolBytes -= evidenceBytes(this.entries[previous]!);
      this.entries[previous] = entry;
    } else {
      this.entries.push(entry);
      this.toolCount += 1;
    }
    this.toolBytes += evidenceBytes(entry);
    while (this.toolCount > limits.entries || this.toolBytes > limits.totalBytes) {
      const oldest = this.entries.findIndex((item) => item.evidence !== undefined);
      if (oldest < 0) break;
      this.toolBytes -= evidenceBytes(this.entries[oldest]!);
      this.entries.splice(oldest, 1);
      this.toolCount -= 1;
      this.omittedCalls += 1;
    }
  }

  recordResult(toolCallId: string, isError?: boolean): void {
    const entry = this.entries.find((item) => item.evidence?.call.toolCallId === toolCallId);
    if (!entry?.evidence || entry.evidence.observation === "blocked") return;
    const observation = isError === undefined ? "unknown" : isError ? "failed" : "succeeded";
    const next: TranscriptEntry = { ...entry, evidence: { ...entry.evidence, observation } };
    this.toolBytes += evidenceBytes(next) - evidenceBytes(entry);
    this.entries[this.entries.indexOf(entry)] = next;
  }

  compacted(): void {
    this.entries = [];
    this.toolBytes = 0;
    this.toolCount = 0;
    this.omittedCalls = 0;
  }

  // A caller deduplicating two seams compares against this, never the raw text:
  // past the entry cap the stored form is clamped, so raw text would never match.
  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return role === "user" ? this.opening : undefined;
  }

  grant(): string | undefined {
    return this.opening;
  }

  snapshot(): TranscriptEntry[] {
    return [
      ...(this.omittedCalls ? [{ role: "tool" as const, text: "", omittedCalls: this.omittedCalls }] : []),
      ...structuredClone(this.entries),
    ];
  }
}

export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map(projectEntry).join("\n");
}

function projectEntry(entry: TranscriptEntry): string {
  if (entry.omittedCalls) return JSON.stringify({ omitted_tool_calls: entry.omittedCalls, reason: "tool history retention limit" });
  if (entry.evidence) return JSON.stringify({ tool_call: entry.evidence });
  const key = entry.role === "tool" ? (entry.tool ?? "tool") : entry.role;
  return JSON.stringify({ [key]: entry.text });
}

function evidenceBytes(entry: TranscriptEntry): number {
  return entry.evidence ? Buffer.byteLength(JSON.stringify(entry.evidence)) : 0;
}
