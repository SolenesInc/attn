// Invariant: `transformText(sourceText) === renderedText`, which annotation text-search
// depends on. Do NOT swap in remark-smartypants, which rewrites `--` everywhere.

import type { Element, Root, RootContent } from "hast";
import type { Text } from "hast";

/** Hand-rolled 32-entry map, NOT the full gemoji set; keep it stable. */
const EMOJI_MAP: Record<string, string> = {
  smile: "😄",
  heart: "❤️",
  thumbsup: "👍",
  thumbsdown: "👎",
  fire: "🔥",
  star: "⭐",
  tada: "🎉",
  rocket: "🚀",
  bug: "🐛",
  sparkles: "✨",
  warning: "⚠️",
  white_check_mark: "✅",
  x: "❌",
  eyes: "👀",
  wave: "👋",
  thinking: "🤔",
  ok: "🆗",
  construction: "🚧",
  boom: "💥",
  gear: "⚙️",
  hourglass: "⏳",
  zap: "⚡",
  lock: "🔒",
  unlock: "🔓",
  memo: "📝",
  book: "📖",
  package: "📦",
  hammer: "🔨",
  checkered_flag: "🏁",
  question: "❓",
  exclamation: "❗",
  bulb: "💡",
};

export function replaceEmojiShortcodes(text: string): string {
  return text.replace(/:([a-z_]+):/g, (match, code: string) => EMOJI_MAP[code] ?? match);
}

export function applySmartPunctuation(text: string, prevChar = ""): string {
  const opensAtStart = prevChar === "" || /[\s([{]/.test(prevChar);
  const openOrKeep = (match: string, pre: string, open: string): string =>
    pre === "" && !opensAtStart ? match : pre + open;
  return text
    .replace(/\.{3}/g, "…")
    .replace(/---/g, "—") // em dash
    .replace(/(\d)--(?=\d)/g, "$1–") // en dash: NUMERIC RANGES ONLY — never --flags
    .replace(/(^|[\s([{])"/g, (m, pre: string) => openOrKeep(m, pre, "“")) // opening double quote
    .replace(/"/g, "”") // remaining doubles close
    .replace(/(^|[\s([{])'/g, (m, pre: string) => openOrKeep(m, pre, "‘")) // opening single quote
    .replace(/'/g, "’"); // remaining singles close / apostrophe
}

export function transformText(text: string, prevChar = ""): string {
  return applySmartPunctuation(replaceEmojiShortcodes(text), prevChar);
}

/** Subtrees whose text must NEVER be transformed: verbatim and non-prose. */
const SKIP_TAGS = new Set([
  "code",
  "pre",
  "kbd",
  "samp",
  "var",
  "script",
  "style",
  "textarea",
  "title",
  "svg",
  "math",
]);

function isMathLike(node: Element): boolean {
  const className: unknown = node.properties?.className;
  const classes = Array.isArray(className)
    ? className.map(String)
    : typeof className === "string"
      ? className.split(/\s+/)
      : [];
  return classes.some((c) => c === "math" || c.startsWith("math-") || c.startsWith("katex"));
}

function isElement(node: Root | RootContent): node is Element {
  return node.type === "element";
}

function isText(node: RootContent): node is Text {
  return node.type === "text";
}

function trailingTextChar(node: RootContent): string | null {
  if (isText(node)) {
    return node.value.length > 0 ? node.value.slice(-1) : null;
  }
  if (isElement(node)) {
    if (node.tagName === "br") {
      return "\n";
    }
    for (let i = node.children.length - 1; i >= 0; i--) {
      const char = trailingTextChar(node.children[i]);
      if (char !== null) {
        return char;
      }
    }
  }
  return null;
}

export default function rehypeProseTransforms() {
  return (tree: Root): void => {
    const ctx = { prev: "" };
    const walk = (node: Root | RootContent): void => {
      if (isElement(node) && (SKIP_TAGS.has(node.tagName) || isMathLike(node))) {
        return;
      }
      if ("children" in node) {
        for (const child of node.children) {
          if (isText(child)) {
            child.value = transformText(child.value, ctx.prev);
            if (child.value.length > 0) {
              ctx.prev = child.value.slice(-1);
            }
          } else {
            walk(child);
            const char = trailingTextChar(child);
            if (char !== null) {
              ctx.prev = char;
            }
          }
        }
      }
    };
    walk(tree);
  };
}
