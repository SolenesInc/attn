import type { CSSProperties } from "react";
import { renderPrompt } from "../prompts/render";

export interface QuickLabel {
  id: string;
  emoji: string;
  text: string;
  color: string;
  tip?: string;
}

const LABEL_COLORS: Record<string, string> = {
  blue: "var(--color-syntax-function)",
  red: "var(--color-error)",
  orange: "var(--color-syntax-number)",
  yellow: "var(--color-syntax-type)",
  purple: "var(--color-merged)",
  teal: "var(--color-syntax-atom)",
  pink: "var(--color-syntax-keyword)",
  green: "var(--color-success)",
  cyan: "var(--color-accent)",
  amber: "var(--color-warning)",
};

export function labelChipStyle(label: QuickLabel): CSSProperties | undefined {
  const color = LABEL_COLORS[label.color];
  return color
    ? {
        background: `color-mix(in srgb, ${color} 15%, transparent)`,
        color: `color-mix(in srgb, ${color} 45%, var(--color-text-primary))`,
      }
    : undefined;
}

export const QUICK_LABEL_GROUPS: QuickLabel[][] = [
  [
    { id: "i-agree", emoji: "👍", text: "I agree", color: "green" },
    { id: "exactly-this", emoji: "💯", text: "Exactly this", color: "green" },
  ],
  [
    { id: "this-is-wrong", emoji: "❌", text: "This is wrong", color: "red" },
    {
      id: "dont-love-this",
      emoji: "😕",
      text: "I don't love this",
      color: "orange",
    },
  ],
  [
    { id: "clarify-this", emoji: "❓", text: "Clarify this", color: "yellow" },
    {
      id: "verify-this",
      emoji: "🔍",
      text: "Verify this",
      color: "blue",
      tip: renderPrompt("annotation-label", "verify-this"),
    },
    {
      id: "show-the-receipt",
      emoji: "🧾",
      text: "Show the receipt",
      color: "teal",
      tip: renderPrompt("annotation-label", "show-the-receipt"),
    },
    {
      id: "give-me-an-example",
      emoji: "🔬",
      text: "Give me an example",
      color: "cyan",
      tip: renderPrompt("annotation-label", "give-me-an-example"),
    },
    {
      id: "consider-alternatives",
      emoji: "🔄",
      text: "Consider alternatives",
      color: "pink",
      tip: renderPrompt("annotation-label", "consider-alternatives"),
    },
  ],
  [{ id: "cut-this", emoji: "🪓", text: "Cut this", color: "amber" }],
  [
    { id: "your-call", emoji: "🪙", text: "Your call", color: "green" },
    { id: "ask-me-first", emoji: "🙋", text: "Ask me first", color: "yellow" },
  ],
];

export const QUICK_LABELS: QuickLabel[] = QUICK_LABEL_GROUPS.flat();

const PROMOTED_LABEL_IDS = [
  "i-agree",
  "this-is-wrong",
  "clarify-this",
] as const;

function requireQuickLabel(id: string): QuickLabel {
  const label = labelById(id);
  if (!label) {
    throw new Error(
      `A promoted toolbar button points at quick label "${id}", which the shared set no longer offers. ` +
        `Point it at one of: ${QUICK_LABELS.map((candidate) => candidate.id).join(", ")}.`,
    );
  }
  return label;
}

export const PROMOTED_LABELS: readonly QuickLabel[] =
  PROMOTED_LABEL_IDS.map(requireQuickLabel);

const promotedLabelIds = new Set<string>(PROMOTED_LABEL_IDS);

const pickerGroups: QuickLabel[][] = [];
for (const group of QUICK_LABEL_GROUPS) {
  const pickerGroup = group.filter((label) => !promotedLabelIds.has(label.id));
  if (pickerGroup.length > 0) {
    pickerGroups.push(pickerGroup);
  }
}
export const QUICK_LABEL_PICKER_GROUPS: readonly (readonly QuickLabel[])[] =
  pickerGroups;

export const QUICK_LABEL_PICKER_LABELS: readonly QuickLabel[] =
  QUICK_LABEL_PICKER_GROUPS.flat();

export function labelByEmoji(emoji: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.emoji === emoji);
}

export function labelById(id: string): QuickLabel | undefined {
  return QUICK_LABELS.find((label) => label.id === id);
}
