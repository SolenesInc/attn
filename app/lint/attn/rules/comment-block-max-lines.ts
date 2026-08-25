import { defineRule } from "@oxlint/plugins";
import type { SourceCode } from "@oxlint/plugins";

export const MAX_LINES = 2;

type Block = { start: number; end: number; startLine: number; endLine: number };

const directive =
  /^\s*(\/\/\/\s*<reference|\/\/\s*(@ts-|eslint-|oxlint-|@vitest-environment|@jsx))/u;

// Adjacent own-line comments form one block; a directive line or a trailing comment breaks it.
function blocks(sourceCode: SourceCode): Block[] {
  const out: Block[] = [];
  let cur: Block | null = null;
  for (const c of sourceCode.getAllComments()) {
    const startLoc = sourceCode.getLocFromIndex(c.start);
    const endLine = sourceCode.getLocFromIndex(c.end).line;
    const lineText = sourceCode.lines[startLoc.line - 1] ?? "";
    const ownLine = lineText.slice(0, startLoc.column).trim() === "";
    if (!ownLine || directive.test(lineText)) {
      cur = null;
      continue;
    }
    if (cur && startLoc.line === cur.endLine + 1) {
      cur.end = c.end;
      cur.endLine = endLine;
      continue;
    }
    cur = { start: c.start, end: c.end, startLine: startLoc.line, endLine };
    out.push(cur);
  }
  return out;
}

export const commentBlockMaxLinesRule = defineRule({
  meta: {
    type: "problem",
    docs: { description: `A comment block may span at most ${MAX_LINES} lines.` },
    messages: {
      tooLong:
        "Comment block spans {{lines}} lines; the limit is {{max}}. Compress it or delete it.",
    },
  },
  create(context) {
    return {
      Program() {
        const { sourceCode } = context;
        for (const b of blocks(sourceCode)) {
          const lines = b.endLine - b.startLine + 1;
          if (lines <= MAX_LINES) continue;
          context.report({
            loc: { start: sourceCode.getLocFromIndex(b.start), end: sourceCode.getLocFromIndex(b.end) },
            messageId: "tooLong",
            data: { lines: String(lines), max: String(MAX_LINES) },
          });
        }
      },
    };
  },
});
