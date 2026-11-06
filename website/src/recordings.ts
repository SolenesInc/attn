export const recordings = {
  'queue-loop': { caption: 'Prompt, settle, and attn brings the next agent', seed: 's-kztv1w' },
  callouts: { caption: 'An agent points at the part of the app it means', seed: 's-779vg6' },
  'usage-analytics': { caption: 'See how you and your agents spent the week', seed: 's-779vg6' },
  crew: { caption: 'A permanent agent wakes with its charter and its last letter', seed: 's-779vg6' },
  launcher: { caption: 'Open an agent from anywhere with the launcher', seed: 's-779vg6' },
  outposts: { caption: 'Run a terminal on another computer from the same app', seed: 's-779vg6' },
  'quick-capture': { caption: 'Catch a thought without leaving what you are doing', seed: 's-779vg6' },
  asks: { caption: 'An agent asks you a question and keeps working', seed: 's-779vg6' },
} satisfies Record<string, { caption: string; seed: string }>;

export type RecordingId = keyof typeof recordings;
