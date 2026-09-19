import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  DenialLedger,
  denialLedgerEnvVar,
  denialLedgerFileName,
  denialLedgerFor,
  denialLedgerPath,
  denialLedgerSessionEnvVar,
  type DenialLedgerRecord,
  type DenialLike,
} from "../automode/ledger";

function tempPath(name = denialLedgerFileName): string {
  return join(mkdtempSync(join(tmpdir(), "attn-ledger-")), name);
}

function readRecords(path: string): DenialLedgerRecord[] {
  return readLines(path)
    .map((line) => JSON.parse(line) as DenialLedgerRecord & { type?: string })
    .filter((record) => record.type !== "rotated");
}

function readMarkers(path: string): { dropped: number }[] {
  return readLines(path)
    .map((line) => JSON.parse(line) as { type?: string; dropped: number })
    .filter((record) => record.type === "rotated");
}

/** What the Go reader computes, so the writer is pinned against its consumer. */
function droppedAcrossGenerations(path: string): number {
  return [`${path}.1`, path]
    .flatMap((generation) => (existsSync(generation) ? readMarkers(generation) : []))
    .reduce((total, marker) => total + marker.dropped, 0);
}

function readLines(path: string): string[] {
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line.trim() !== "");
}

function denial(overrides: Partial<DenialLike> = {}): DenialLike {
  return {
    toolCallId: "call-1",
    tool: "bash",
    action: "bash: git push --force origin main",
    reason: "not asked for",
    rule: "guardian",
    at: "2026-08-18T10:00:00.000Z",
    ...overrides,
  };
}

describe("where the ledger lives", () => {
  test("attn names the file; bare pi falls back to pi's config directory", () => {
    expect(denialLedgerPath({ [denialLedgerEnvVar]: "/data/attn-dev/denials.jsonl" })).toBe(
      "/data/attn-dev/denials.jsonl",
    );
    expect(denialLedgerPath({ PI_CODING_AGENT_DIR: "/tmp/pi" })).toBe(join("/tmp/pi", denialLedgerFileName));
    expect(denialLedgerPath({})).toContain(join(".pi", "agent", denialLedgerFileName));
  });

  test("the session id rides along, and a bare pi has none to name", () => {
    const path = tempPath();
    denialLedgerFor({ [denialLedgerEnvVar]: path, [denialLedgerSessionEnvVar]: " sess-7 " }).record(denial());
    expect(readRecords(path)[0]?.session_id).toBe("sess-7");

    const bare = tempPath();
    denialLedgerFor({ [denialLedgerEnvVar]: bare }).record(denial());
    expect(readRecords(bare)[0]?.session_id).toBe("");
  });
});

describe("what a rejection leaves behind", () => {
  test("the record carries what the Go reader asks for", () => {
    const path = tempPath();
    new DenialLedger(path, "sess-1").record(denial());
    expect(readRecords(path)).toEqual([{
      session_id: "sess-1",
      tool_call_id: "call-1",
      tool: "bash",
      action: "bash: git push --force origin main",
      reason: "not asked for",
      rule: "guardian",
      at: "2026-08-18T10:00:00.000Z",
    }]);
  });

  test("a rejection user approval cannot clear says so; an arguable one stays silent", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1");
    ledger.record(denial({ rule: "forbidden", clearable: false }));
    ledger.record(denial({ toolCallId: "call-2" }));
    const records = readRecords(path);
    expect(records[0]?.clearable).toBe(false);
    expect(records[1]).not.toHaveProperty("clearable");
  });

  test("filters credentials before writing a rejection to disk", () => {
    const path = tempPath();
    const token = `ghp_${"z".repeat(36)}`;
    new DenialLedger(path, "synthetic-session").record(denial({
      action: `bash: echo ${token}`,
      reason: `Refused ${token}`,
    }));
    const saved = readFileSync(path, "utf8");
    expect(saved).not.toContain(token);
    expect(saved).toContain("REDACTED");
  });
});

describe("what the ledger admits it lost", () => {
  test("the first rotation drops nothing, and says nothing", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1", 100);

    ledger.record(denial({ toolCallId: "one" }));
    ledger.record(denial({ toolCallId: "two" }));

    expect(readRecords(`${path}.1`).map((record) => record.tool_call_id)).toEqual(["one"]);
    expect(readRecords(path).map((record) => record.tool_call_id)).toEqual(["two"]);
    expect(readMarkers(path)).toEqual([]);
    expect(droppedAcrossGenerations(path)).toBe(0);
  });

  // The reader sums markers across BOTH generations, so a marker folding into an
  // earlier one is counted twice and compounds with every rotation.
  test("a dropped generation is counted once, however many rotations came before", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1", 100);

    for (const id of ["one", "two", "three", "four"]) ledger.record(denial({ toolCallId: id }));

    expect(readRecords(`${path}.1`).map((record) => record.tool_call_id)).toEqual(["three"]);
    expect(readRecords(path).map((record) => record.tool_call_id)).toEqual(["four"]);
    expect(droppedAcrossGenerations(path)).toBe(2);

    ledger.record(denial({ toolCallId: "five" }));
    expect(readRecords(`${path}.1`).map((record) => record.tool_call_id)).toEqual(["four"]);
    expect(readRecords(path).map((record) => record.tool_call_id)).toEqual(["five"]);
    expect(droppedAcrossGenerations(path)).toBe(3);
  });

  test("a generation another session rotated out from under this one costs no record", () => {
    const path = tempPath();
    const ledger = new DenialLedger(path, "sess-1", 100);
    ledger.record(denial({ toolCallId: "one" }));
    renameSync(path, `${path}.1`);

    expect(() => ledger.record(denial({ toolCallId: "two" }))).not.toThrow();
    expect(readRecords(path).map((record) => record.tool_call_id)).toEqual(["two"]);
  });

  test("a marker is not a rejection, and a loss already claimed is not claimed twice", () => {
    const path = tempPath();
    writeFileSync(path, `${JSON.stringify({ type: "rotated", dropped: 3, at: "2026-08-18T10:00:00.000Z" })}\n`);
    const ledger = new DenialLedger(path, "sess-1", 1);

    ledger.record(denial());

    expect(readRecords(path)).toHaveLength(1);
    expect(readMarkers(path)).toEqual([]);
    expect(droppedAcrossGenerations(path)).toBe(3);

    ledger.record(denial({ toolCallId: "second" }));
    expect(droppedAcrossGenerations(path)).toBe(3);
  });
});
