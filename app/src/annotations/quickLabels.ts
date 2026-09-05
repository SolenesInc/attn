import { renderPrompt } from "../prompts/render";

export interface QuickLabel {
  id: string;
  emoji: string;
  text: string;
  color: string;
  tip?: string;
}

export const LABEL_COLOR_MAP: Record<
  string,
  { bg: string; text: string; darkText: string }
> = {
  blue: { bg: "rgba(59,130,246,0.15)", text: "#2563eb", darkText: "#60a5fa" },
  red: { bg: "rgba(239,68,68,0.15)", text: "#dc2626", darkText: "#f87171" },
  orange: { bg: "rgba(249,115,22,0.15)", text: "#ea580c", darkText: "#fb923c" },
  yellow: { bg: "rgba(234,179,8,0.15)", text: "#ca8a04", darkText: "#facc15" },
  purple: { bg: "rgba(147,51,234,0.15)", text: "#9333ea", darkText: "#a78bfa" },
  teal: { bg: "rgba(20,184,166,0.15)", text: "#0d9488", darkText: "#2dd4bf" },
  pink: { bg: "rgba(236,72,153,0.15)", text: "#db2777", darkText: "#f472b6" },
  green: { bg: "rgba(34,197,94,0.15)", text: "#16a34a", darkText: "#4ade80" },
  cyan: { bg: "rgba(8,145,178,0.15)", text: "#0891b2", darkText: "#22d3ee" },
  amber: { bg: "rgba(180,83,9,0.15)", text: "#b45309", darkText: "#fbbf24" },
};

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
