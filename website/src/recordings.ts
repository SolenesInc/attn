export const recordings = {
  'queue-loop': { caption: 'Prompt, settle, and attn brings the next agent', seed: 's-kztv1w' },
  callouts: { caption: 'An agent points at a part of the app, and you step through what it says', seed: 's-779vg6' },
  'usage-analytics': { caption: 'Settings → Analytics lists every record, exactly as it is stored and sent', seed: 's-779vg6' },
  crew: { caption: 'A crew member wakes from its charter and its last letter, and greets you', seed: 's-779vg6' },
  launcher: { caption: 'Pick a project and a worktree, and start an agent from one dialog', seed: 's-779vg6' },
  outposts: { caption: 'An agent on another computer runs in a tile beside your local ones', seed: 's-779vg6' },
  'quick-capture': { caption: 'Send a message to the Chief from another app, and go back to what you were doing', seed: 's-779vg6' },
  asks: { caption: 'An agent sends a question, and you answer it from the Asks drawer', seed: 's-779vg6' },
} satisfies Record<string, { caption: string; seed: string }>;

export type RecordingId = keyof typeof recordings;
