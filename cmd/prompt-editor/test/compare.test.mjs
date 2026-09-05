import assert from "node:assert/strict";
import test from "node:test";
import { diffRows, mergedRecipients, sourceChange } from "../web/compare.js";

test("unified hunks retain line numbers, blank lines, and newline notices", () => {
  const rows = diffRows("diff --git a/base b/working\n--- a/base\n+++ b/working\n@@ -2,2 +2,3 @@\n same\n-old\n+new\n+\n\\ No newline at end of file\n");
  assert.deepEqual(rows.slice(1).map(({ kind, oldLine, newLine, text }) => ({ kind, oldLine, newLine, text })), [
    { kind: "context", oldLine: 2, newLine: 2, text: "same" },
    { kind: "removed", oldLine: 3, newLine: undefined, text: "old" },
    { kind: "added", oldLine: undefined, newLine: 3, text: "new" },
    { kind: "added", oldLine: undefined, newLine: 4, text: "" },
    { kind: "notice", oldLine: undefined, newLine: undefined, text: " No newline at end of file" },
  ]);
});

test("navigation retains removed events without replacing current definitions", () => {
  const current = [{ id: "crew", events: [{ id: "wake", description: "current" }] }];
  const base = [{ id: "crew", events: [{ id: "wake", description: "base" }, { id: "retired" }] }, { id: "removed", events: [] }];
  const merged = mergedRecipients(current, base);
  assert.deepEqual(merged[0].events, [current[0].events[0], base[0].events[1]]);
  assert.equal(merged[1].id, "removed");
  assert.equal(current[0].events.length, 1);
});

test("source changes distinguish missing and empty files and include drafts", () => {
  const current = { sources: { empty: { text: "" }, same: { text: "same" } } };
  const base = { sources: { same: { text: "same" }, removed: { text: "" } } };
  assert.equal(sourceChange(current, base, "empty", ""), "added");
  assert.equal(sourceChange(current, base, "removed", ""), "removed");
  assert.equal(sourceChange(current, base, "same", "draft"), "modified");
  assert.equal(sourceChange(current, base, "same", "same"), "");
});
