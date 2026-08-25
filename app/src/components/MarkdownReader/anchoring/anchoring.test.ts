// `./rebase` is spy-wrapped pass-through only, so the hash-unchanged path's
// zero-heuristic guarantee stays observable.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createAnchor } from "./create";
import { extractBlockTexts } from "./extractBlocks";
import { fnv1a32 } from "./hash";
import { rebaseAnchor } from "./rebase";
import { resolveAnchor, resolveOrRebase } from "./resolve";
import type { AnchorRecord, BlockText } from "./types";

vi.mock("./rebase", { spy: true });

beforeEach(() => {
  vi.mocked(rebaseAnchor).mockClear();
});

/** FIRST deepest stamped block whose text contains `needle` (spec §8 order). */
function blockContaining(content: string, needle: string): BlockText {
  const hits = extractBlockTexts(content).filter((b) => b.text.includes(needle));
  if (hits.length === 0) {
    throw new Error(`no block contains ${JSON.stringify(needle)}`);
  }
  return hits.reduce((a, b) => (b.depth > a.depth ? b : a));
}

function anchorFor(content: string, needle: string): AnchorRecord {
  const block = blockContaining(content, needle);
  const start = block.text.indexOf(needle);
  const anchor = createAnchor(content, block.blockId, start, start + needle.length);
  if (!anchor) {
    throw new Error(`createAnchor failed for ${JSON.stringify(needle)}`);
  }
  expectInvariants(anchor);
  return anchor;
}

function expectInvariants(anchor: AnchorRecord): void {
  expect(anchor.end - anchor.start).toBe(anchor.exact.length);
  expect(anchor.exact.trim()).not.toBe("");
  expect(anchor.prefix.length).toBeLessThanOrEqual(32);
  expect(anchor.suffix.length).toBeLessThanOrEqual(32);
}

function expectBaselined(anchor: AnchorRecord, content: string): void {
  expectInvariants(anchor);
  expect(anchor.contentHash).toBe(fnv1a32(content));
  const block = extractBlockTexts(content).find((b) => b.blockId === anchor.blockId);
  expect(block).toBeDefined();
  expect(block!.text.slice(anchor.start, anchor.end)).toBe(anchor.exact);
  expect(block!.startLine).toBe(anchor.startLine);
  expect(block!.endLine).toBe(anchor.endLine);
  const again = resolveOrRebase(content, anchor);
  expect(again).toEqual({
    state: "exact",
    blockId: anchor.blockId,
    start: anchor.start,
    end: anchor.end,
    anchor,
  });
}

describe("hash-unchanged exactness (zero heuristics)", () => {
  const DOC = [
    "Repeated sentence appears twice in this document.",
    "",
    "Repeated sentence appears twice in this document.",
    "",
    "A closing paragraph.",
  ].join("\n");

  it("returns stored coordinates without ever invoking rebase", () => {
    const blocks = extractBlockTexts(DOC);
    const second = blocks.filter((b) => b.text.includes("Repeated sentence"))[1];
    const anchor = createAnchor(DOC, second.blockId, 0, second.text.length)!;
    vi.mocked(rebaseAnchor).mockClear();

    const resolved = resolveAnchor(DOC, anchor);
    expect(resolved).toEqual({
      state: "exact",
      blockId: second.blockId,
      start: 0,
      end: second.text.length,
    });
    expect(rebaseAnchor).not.toHaveBeenCalled();

    const full = resolveOrRebase(DOC, anchor);
    expect(full.state).toBe("exact");
    expect(rebaseAnchor).not.toHaveBeenCalled();
  });

  it("corrupted offsets with a matching hash warn and fall through to rebase", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      const anchor = anchorFor(DOC, "A closing paragraph.");
      const corrupted: AnchorRecord = { ...anchor, start: anchor.start + 2, end: anchor.end + 2 };

      const result = resolveOrRebase(DOC, corrupted);
      expect(warn).toHaveBeenCalled();
      expect(rebaseAnchor).toHaveBeenCalledTimes(1);
      expect(result.state).toBe("rebased");
      if (result.state !== "rebased") throw new Error("unreachable");
      expect(result.start).toBe(anchor.start);
      expect(result.end).toBe(anchor.end);
      expectBaselined(result.anchor, DOC);
    } finally {
      warn.mockRestore();
    }
  });

  it("corrupted offsets whose exact exists nowhere orphan as offset-mismatch", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      const anchor = anchorFor(DOC, "A closing paragraph.");
      const corrupted: AnchorRecord = { ...anchor, exact: "text that never existed anywhere" };
      expect(resolveOrRebase(DOC, corrupted)).toEqual({
        state: "orphan",
        reason: "offset-mismatch",
      });
    } finally {
      warn.mockRestore();
    }
  });

  it("matching hash but vanished blockId orphans as block-missing when unrecoverable", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      const anchor = anchorFor(DOC, "A closing paragraph.");
      const ghost: AnchorRecord = {
        ...anchor,
        blockId: "b99-paragraph",
        exact: "text that never existed anywhere",
      };
      expect(resolveOrRebase(DOC, ghost)).toEqual({ state: "orphan", reason: "block-missing" });
    } finally {
      warn.mockRestore();
    }
  });
});

describe("duplicated paragraphs", () => {
  it("disambiguates by prefix/suffix when the blockId drifted onto another block", () => {
    const DOC = [
      "Intro paragraph.",
      "",
      "Alpha before. The shared sentence sits here. Alpha after.",
      "",
      "Filler in the middle.",
      "",
      "Beta before. The shared sentence sits here. Beta after.",
      "",
      "Outro paragraph.",
    ].join("\n");
    const betaBlock = blockContaining(DOC, "Beta before.");
    const needle = "The shared sentence sits here.";
    const betaStart = betaBlock.text.indexOf(needle);
    const anchor = createAnchor(DOC, betaBlock.blockId, betaStart, betaStart + needle.length)!;
    expectInvariants(anchor);
    expect(anchor.startLine).toBe(7);
    expect(anchor.prefix).toBe("Beta before. ");

    const EDITED = "Inserted at the very top.\n\n" + DOC;
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.startLine).toBe(9);
    expect(result.anchor.prefix).toBe("Beta before. ");
    expect(result.anchor.exact).toBe("The shared sentence sits here.");
    expectBaselined(result.anchor, EDITED);
  });

  it("orphans identical duplicates when line proximity is not decisive", () => {
    const DOC = [
      "Intro.",
      "",
      "Same duplicated paragraph text.",
      "",
      "Filler line one",
      "still the same filler paragraph",
      "",
      "Same duplicated paragraph text.",
      "",
      "Tail.",
    ].join("\n");
    const blocks = extractBlockTexts(DOC);
    const second = blocks.filter((b) => b.text === "Same duplicated paragraph text.")[1];
    expect(second.startLine).toBe(8);
    const anchor = createAnchor(DOC, second.blockId, 0, second.text.length)!;
    expect(anchor.prefix).toBe("");
    expect(anchor.suffix).toBe("");

    const EDITED = "Inserted at the very top.\n\n" + DOC;
    expect(resolveOrRebase(EDITED, anchor)).toEqual({ state: "orphan", reason: "ambiguous" });
  });

  it("breaks a tie between identical duplicates when proximity IS decisive", () => {
    const DOC = [
      "Intro.",
      "",
      "Same duplicated paragraph text.",
      "",
      "Same duplicated paragraph text.",
      "",
      "Tail.",
    ].join("\n");
    const blocks = extractBlockTexts(DOC);
    const second = blocks.filter((b) => b.text === "Same duplicated paragraph text.")[1];
    expect(second.startLine).toBe(5);
    const anchor = createAnchor(DOC, second.blockId, 0, second.text.length)!;
    expect(anchor.prefix).toBe("");
    expect(anchor.suffix).toBe("");

    const EDITED = [
      "Intro.",
      "",
      "Filler paragraph.",
      "",
      "Same duplicated paragraph text.",
      "",
      ...Array.from({ length: 40 }, () => ""),
      "Same duplicated paragraph text.",
      "",
      "Tail.",
    ].join("\n");
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.startLine).toBe(5);
    expectBaselined(result.anchor, EDITED);
  });

  it("orphans on an exact score tie between identical duplicates (equidistant copies)", () => {
    const DOC = [
      "Intro.",
      "",
      "One filler paragraph.",
      "",
      "Another filler paragraph.",
      "",
      "Same duplicated paragraph text.",
      "",
      "Same duplicated paragraph text.",
      "",
      "Tail.",
    ].join("\n");
    const blocks = extractBlockTexts(DOC);
    const second = blocks.filter((b) => b.text === "Same duplicated paragraph text.")[1];
    expect(second.startLine).toBe(9);
    const anchor = createAnchor(DOC, second.blockId, 0, second.text.length)!;
    expect(anchor.prefix).toBe("");
    expect(anchor.suffix).toBe("");

    const EDITED = [
      "Intro.",
      "",
      "One filler paragraph.",
      "",
      "Same duplicated paragraph text.",
      "",
      ...Array.from({ length: 6 }, () => ""),
      "Same duplicated paragraph text.",
      "",
      "Tail.",
    ].join("\n");
    const editedBlocks = extractBlockTexts(EDITED);
    const dupLines = editedBlocks
      .filter((b) => b.text === "Same duplicated paragraph text.")
      .map((b) => b.startLine);
    expect(dupLines.map((l) => Math.abs(l - anchor.startLine))).toEqual([4, 4]);
    expect(resolveOrRebase(EDITED, anchor)).toEqual({ state: "orphan", reason: "ambiguous" });
  });

  it("does not adopt an inserted sibling that inherits the anchor's old ordinal blockId", () => {
    const DOC = "Beta before. The shared sentence sits here. Beta after.";
    const needle = "The shared sentence sits here.";
    const anchor = anchorFor(DOC, needle);
    expect(anchor.prefix).toBe("Beta before. ");

    const EDITED = "Intruder before. The shared sentence sits here. Intruder after.\n\n" + DOC;
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.prefix).toBe("Beta before. ");
    expect(result.anchor.exact).toBe(needle);
    expectBaselined(result.anchor, EDITED);
  });

  it("orphans as ambiguous when duplicates are indistinguishable and both far away", () => {
    const OLD = [
      "Other intro.",
      "",
      "Nothing to see in this filler.",
      "",
      "The quick brown fox says the target phrase lives here today.",
    ].join("\n");
    const anchor = anchorFor(OLD, "the target phrase lives here");

    const NEW = [
      "Other intro.",
      "",
      "Nothing to see in this filler.",
      "",
      "A second filler so the stored blockId lands on a non-matching block.",
      "",
      ...Array.from({ length: 300 }, () => ""),
      "zz zz zz the target phrase lives here zz zz zz",
      "",
      ...Array.from({ length: 20 }, () => ""),
      "zz zz zz the target phrase lives here zz zz zz",
    ].join("\n");
    expect(resolveOrRebase(NEW, anchor)).toEqual({ state: "orphan", reason: "ambiguous" });
  });
});

describe("edits to the annotated sentence", () => {
  const DOC = [
    "Intro paragraph here.",
    "",
    "The deploy step uses the flag cache and retries twice.",
    "",
    "Tail paragraph here.",
  ].join("\n");

  it("survives a slight rewrite around the anchored phrase (same-block tier)", () => {
    const anchor = anchorFor(DOC, "the flag cache");
    const EDITED = DOC.replace(
      "The deploy step uses the flag cache and retries twice.",
      "The deploy step now uses the flag cache and retries three times.",
    );
    const rebased = rebaseAnchor(anchor, EDITED);
    expect(rebased.state).toBe("rebased");
    if (rebased.state !== "rebased") throw new Error("unreachable");
    expect(rebased.tier).toBe("same-block");
    expect(rebased.anchor.exact).toBe("the flag cache");
    expect(rebased.anchor.prefix).toBe("The deploy step now uses ");
    expect(rebased.anchor.suffix).toBe(" and retries three times.");
    expect(rebased.anchor.start).toBe(anchor.start + "now ".length);
    expectBaselined(rebased.anchor, EDITED);
  });

  it("orphans when the sentence is rewritten beyond recognition", () => {
    const anchor = anchorFor(DOC, "the flag cache");
    const EDITED = DOC.replace(
      "The deploy step uses the flag cache and retries twice.",
      "Deployment is configured elsewhere entirely.",
    );
    expect(resolveOrRebase(EDITED, anchor)).toEqual({
      state: "orphan",
      reason: "text-not-found",
    });
  });

  it("orphans when the block is deleted entirely", () => {
    const anchor = anchorFor(DOC, "the flag cache");
    const EDITED = ["Intro paragraph here.", "", "Tail paragraph here."].join("\n");
    expect(resolveOrRebase(EDITED, anchor)).toEqual({
      state: "orphan",
      reason: "text-not-found",
    });
  });
});

describe("insertions above (line shift, offsets stable)", () => {
  it("re-baselines startLine after 20 prepended blank lines, tier same-block", () => {
    const DOC = "A stable paragraph to annotate right here.";
    const anchor = anchorFor(DOC, "paragraph to annotate");
    expect(anchor.startLine).toBe(1);

    const EDITED = "\n".repeat(20) + DOC;
    const rebased = rebaseAnchor(anchor, EDITED);
    expect(rebased.state).toBe("rebased");
    if (rebased.state !== "rebased") throw new Error("unreachable");
    expect(rebased.tier).toBe("same-block");
    expect(rebased.anchor.startLine).toBe(21);
    expect(rebased.anchor.start).toBe(anchor.start);
    expect(rebased.anchor.end).toBe(anchor.end);
    expect(rebased.anchor.exact).toBe(anchor.exact);
    expect(rebased.anchor.blockId).toBe(anchor.blockId);
    expect(rebased.anchor.contentHash).not.toBe(anchor.contentHash);
    expectBaselined(rebased.anchor, EDITED);
  });
});

describe("prose transforms in anchor space", () => {
  it("anchors rendered smart punctuation (curly quotes, em/en dash, ellipsis, emoji)", () => {
    const DOC = ['He said "wait --- pages 3--5 cover it..." :rocket:', "", "Another paragraph."].join(
      "\n",
    );
    const rendered = "“wait — pages 3–5 cover it…” 🚀";
    const block = blockContaining(DOC, "He said");
    expect(block.text).toBe(`He said ${rendered}`);

    const anchor = anchorFor(DOC, rendered);
    expect(anchor.exact).toBe(rendered);
    expect(resolveAnchor(DOC, anchor)).toEqual({
      state: "exact",
      blockId: anchor.blockId,
      start: anchor.start,
      end: anchor.end,
    });

    const EDITED = DOC.replace("Another paragraph.", "Another paragraph, edited.");
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.exact).toBe(rendered);
    expectBaselined(result.anchor, EDITED);
  });

  it("leaves -- untouched in code and in prose flags (digit-range rule only)", () => {
    const DOC = "Run `bun --watch` or pass --verbose to see pages 3--5.";
    const block = blockContaining(DOC, "Run");
    expect(block.text).toBe("Run bun --watch or pass --verbose to see pages 3–5.");

    const anchor = anchorFor(DOC, "bun --watch");
    expect(anchor.exact).toBe("bun --watch");
    const resolved = resolveAnchor(DOC, anchor);
    expect(resolved.state).toBe("exact");
  });

  it("anchors emoji shortcodes as their rendered emoji", () => {
    const DOC = "Ship it :rocket: then celebrate :tada: loudly.";
    const anchor = anchorFor(DOC, "🚀 then celebrate 🎉");
    expect(anchor.exact).toBe("🚀 then celebrate 🎉");
    expect(resolveAnchor(DOC, anchor).state).toBe("exact");
  });
});

describe("inline boundaries and offsets", () => {
  it("spans inline code and emphasis boundaries within one paragraph", () => {
    const DOC = "Use `git status` and **verify** the output carefully.";
    const block = blockContaining(DOC, "git status");
    expect(block.text).toBe("Use git status and verify the output carefully.");

    const anchor = anchorFor(DOC, "status and verify");
    expect(resolveAnchor(DOC, anchor)).toEqual({
      state: "exact",
      blockId: anchor.blockId,
      start: block.text.indexOf("status and verify"),
      end: block.text.indexOf("status and verify") + "status and verify".length,
    });

    const EDITED = DOC + "\n\nA new trailing paragraph.";
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.exact).toBe("status and verify");
    expectBaselined(result.anchor, EDITED);
  });

  it("supports start=0 and end=text.length boundary anchors", () => {
    const DOC = "Whole-block anchor target.";
    const block = blockContaining(DOC, "Whole-block");

    const full = createAnchor(DOC, block.blockId, 0, block.text.length)!;
    expect(full.exact).toBe(block.text);
    expect(full.prefix).toBe("");
    expect(full.suffix).toBe("");
    expect(resolveAnchor(DOC, full).state).toBe("exact");

    const first = createAnchor(DOC, block.blockId, 0, 1)!;
    expect(first.exact).toBe("W");
    expect(resolveAnchor(DOC, first).state).toBe("exact");

    const last = createAnchor(DOC, block.blockId, block.text.length - 1, block.text.length)!;
    expect(last.exact).toBe(".");
    expect(resolveAnchor(DOC, last).state).toBe("exact");
  });

  it("uses UTF-16 code units end-to-end for surrogate pairs", () => {
    const DOC = "👍👍 ship :rocket: now";
    const block = blockContaining(DOC, "ship");
    expect(block.text).toBe("👍👍 ship 🚀 now");
    expect(block.text.length).toBe(16); // 2+2 (thumbs) + 1 + 4 + 1 + 2 (rocket) + 4

    const anchor = createAnchor(DOC, block.blockId, 5, 12)!; // "ship 🚀"
    expect(anchor.exact).toBe("ship 🚀");
    expect(anchor.exact.length).toBe(7); // surrogate pair counts as 2
    expect(anchor.prefix).toBe("👍👍 ");
    expect(anchor.suffix).toBe(" now");
    expect(resolveAnchor(DOC, anchor)).toEqual({
      state: "exact",
      blockId: block.blockId,
      start: 5,
      end: 12,
    });

    const EDITED = DOC + "\n\nTail paragraph.";
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.start).toBe(5);
    expect(result.end).toBe(12);
    expectBaselined(result.anchor, EDITED);
  });

  it("preserves combining marks without NFC normalization", () => {
    const DOC = "A café latte order."; // e + combining acute, NOT é
    const block = blockContaining(DOC, "latte");
    expect(block.text).toBe("A café latte order.");
    expect(block.text).not.toContain("é");

    const anchor = anchorFor(DOC, "café");
    expect(anchor.exact.length).toBe(5);
    expect(resolveAnchor(DOC, anchor).state).toBe("exact");
  });
});

describe("single-block contract", () => {
  const DOC = ["First block ends here.", "", "Second block starts now."].join("\n");

  it("createAnchor rejects a range that overruns the block (cross-block selection)", () => {
    const first = blockContaining(DOC, "First block");
    expect(createAnchor(DOC, first.blockId, 0, first.text.length + 5)).toBeNull();
  });

  it("rebase of a hand-built cross-block record orphans", () => {
    const first = blockContaining(DOC, "First block");
    const crossBlock: AnchorRecord = {
      blockId: first.blockId,
      startLine: 1,
      endLine: 3,
      exact: "ends here.Second block",
      prefix: "First block ",
      suffix: " starts now.",
      start: 12,
      end: 34,
      contentHash: "00000000",
    };
    expect(resolveOrRebase(DOC, crossBlock)).toEqual({
      state: "orphan",
      reason: "text-not-found",
    });
  });
});

describe("structural changes", () => {
  it("re-attributes to the deepest owner when a paragraph becomes a list item", () => {
    const DOC = ["# Head", "", "Target sentence lives here.", "", "Tail paragraph."].join("\n");
    const anchor = anchorFor(DOC, "Target sentence lives here.");
    expect(anchor.blockId).toMatch(/-paragraph$/);

    const EDITED = ["# Head", "", "- Target sentence lives here.", "", "Tail paragraph."].join("\n");
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.blockId).toMatch(/-list-item$/);
    expect(result.anchor.exact).toBe("Target sentence lives here.");
    expect(result.anchor.start).toBe(0);
    expectBaselined(result.anchor, EDITED);
  });

  it("anchors alert body text with the marker stripped and lines including it", () => {
    const DOC = ["> [!NOTE]", "> Remember to hydrate the cache."].join("\n");
    const blocks = extractBlockTexts(DOC);
    const alert = blocks.find((b) => b.text.includes("Remember to hydrate"))!;
    expect(alert.blockId).toMatch(/-blockquote$/);
    expect(alert.text).not.toContain("[!NOTE]");
    expect(alert.startLine).toBe(1);
    expect(alert.endLine).toBe(2);

    const anchor = anchorFor(DOC, "hydrate the cache");
    expect(resolveAnchor(DOC, anchor).state).toBe("exact");

    const EDITED = "Intro line.\n\n" + DOC;
    const result = resolveOrRebase(EDITED, anchor);
    expect(result.state).toBe("rebased");
    if (result.state !== "rebased") throw new Error("unreachable");
    expect(result.anchor.startLine).toBe(3);
    expectBaselined(result.anchor, EDITED);
  });

  it("resolves against pre text with the trailing-newline rule, at the block end", () => {
    const DOC = ["```js", "first();", "second();", "```"].join("\n");
    const block = blockContaining(DOC, "second");
    expect(block.text).toBe("first();\nsecond();");

    const start = block.text.indexOf("second();");
    const anchor = createAnchor(DOC, block.blockId, start, block.text.length)!;
    expect(anchor.end).toBe(block.text.length);
    expect(resolveAnchor(DOC, anchor)).toEqual({
      state: "exact",
      blockId: block.blockId,
      start,
      end: block.text.length,
    });
  });

  it("marks mermaid blocks nonPaintable but still anchors in text space", () => {
    const DOC = ["```mermaid", "graph TD", "A-->B", "```"].join("\n");
    const block = blockContaining(DOC, "graph TD");
    expect(block.nonPaintable).toBe(true);
    expect(block.text).toBe("graph TD\nA-->B");

    const anchor = anchorFor(DOC, "A-->B");
    expect(resolveAnchor(DOC, anchor).state).toBe("exact");
  });

  it("propagates nonPaintable to stamped ancestors of a nested mermaid fence", () => {
    const DOC = [
      "- plain item",
      "- item with diagram",
      "",
      "  ```mermaid",
      "  graph TD",
      "  A-->B",
      "  ```",
      "",
      "- trailing item",
    ].join("\n");
    const blocks = extractBlockTexts(DOC);

    const li = blockContaining(DOC, "graph TD");
    expect(li.nonPaintable).toBe(true);
    const ul = blocks.find((b) => b.blockId === li.parentId)!;
    expect(ul.nonPaintable).toBe(true);

    expect(blockContaining(DOC, "plain item").nonPaintable).toBeUndefined();
    expect(blockContaining(DOC, "trailing item").nonPaintable).toBeUndefined();
  });
});

describe("whitespace-normalized tier", () => {
  it("rescues a rewrapped hard-wrapped paragraph and re-baselines exact to the new raw text", () => {
    const DOC = ["Alpha intro.", "", "wrap line one", "wrap line two", "", "Omega tail."].join("\n");
    const block = blockContaining(DOC, "wrap line one");
    expect(block.text).toBe("wrap line one\nwrap line two");

    const anchor = createAnchor(DOC, block.blockId, 0, block.text.length)!;
    expect(anchor.exact).toContain("\n");

    const EDITED = ["Alpha intro.", "", "wrap line one wrap line two", "", "Omega tail."].join("\n");
    const rebased = rebaseAnchor(anchor, EDITED);
    expect(rebased.state).toBe("rebased");
    if (rebased.state !== "rebased") throw new Error("unreachable");
    expect(rebased.tier).toBe("normalized");
    expect(rebased.anchor.exact).toBe("wrap line one wrap line two");
    expect(rebased.anchor.start).toBe(0);
    expectBaselined(rebased.anchor, EDITED);
  });
});

describe("re-baselining never compounds", () => {
  it("five successive edits + rebases end byte-identical to a fresh anchor", () => {
    let content = [
      "Alpha paragraph one.",
      "",
      "The compounding target sentence.",
      "",
      "Omega paragraph.",
    ].join("\n");
    let anchor = anchorFor(content, "The compounding target sentence.");

    for (let i = 1; i <= 5; i++) {
      content = `Inserted number ${i}.\n\n` + content;
      const result = resolveOrRebase(content, anchor);
      expect(result.state).toBe("rebased");
      if (result.state !== "rebased") throw new Error("unreachable");
      anchor = result.anchor;
      expectBaselined(anchor, content);
    }

    const fresh = createAnchor(content, anchor.blockId, anchor.start, anchor.end);
    expect(anchor).toEqual(fresh);
    expect(anchor.startLine).toBe(13); // 5 insertions × 2 lines + original line 3
  });
});
