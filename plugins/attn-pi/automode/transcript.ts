// Only what the user and the agent SAID: a classifier that reads tool output can be
// talked into a verdict. Tripwires from 6,357 messages (2026-08-17): p90 2,523 chars.

export const transcriptEntryLimit = 12;
export const transcriptEntryCharLimit = 4_000;
export const transcriptCharLimit = 8_000;

export type TranscriptRole = "user" | "assistant";

export type TranscriptEntry = {
  role: TranscriptRole;
  text: string;
};

export class TranscriptWindow {
  private readonly entries: TranscriptEntry[] = [];

  record(role: TranscriptRole, text: string): void {
    const trimmed = text.trim();
    if (trimmed === "") return;
    this.entries.push({ role, text: clampEntry(trimmed) });
    while (this.entries.length > transcriptEntryLimit) this.entries.shift();
  }

  // A caller deduplicating two seams compares against this, never the raw text:
  // past the entry cap the stored form is clamped, so raw text would never match.
  latest(role: TranscriptRole): string | undefined {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (entry?.role === role) return entry.text;
    }
    return undefined;
  }

  snapshot(): TranscriptEntry[] {
    const kept: TranscriptEntry[] = [];
    let budget = transcriptCharLimit;
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const entry = this.entries[i];
      if (!entry) continue;
      if (entry.text.length > budget) break;
      budget -= entry.text.length;
      kept.unshift(entry);
    }
    return kept;
  }
}

export function transcriptEntryText(text: string): string {
  return clampEntry(text.trim());
}

export function renderTranscript(entries: readonly TranscriptEntry[]): string {
  return entries.map((entry) => `[${entry.role}] ${entry.text}`).join("\n");
}

function clampEntry(text: string): string {
  if (text.length <= transcriptEntryCharLimit) return text;
  const half = Math.floor((transcriptEntryCharLimit - 1) / 2);
  return `${text.slice(0, half)}…${text.slice(text.length - half)}`;
}
