// NO-NETWORK INVARIANT: every fetchable URL attribute is either gated by a component
// renderer (img `src`, a `href`) or absent from this schema. Runs BEFORE rehypeAlerts.

import { defaultSchema, type Options } from 'rehype-sanitize';

// Ungated media containers (see the no-network invariant above).
const REMOVED_DEFAULT_TAGS = new Set(['picture', 'source']);

export const readerSanitizeSchema: Options = {
  ...defaultSchema,
  tagNames: [
    ...(defaultSchema.tagNames ?? []).filter((tag) => !REMOVED_DEFAULT_TAGS.has(tag)),
    'abbr',
    'small',
    'mark',
    'article',
    'aside',
    'header',
    'footer',
  ],
  protocols: {
    ...defaultSchema.protocols,
    // `file:` passes here; the img/a renderers still gate it (markdownLinks.ts).
    href: [...(defaultSchema.protocols?.href ?? []), 'file'],
    src: [...(defaultSchema.protocols?.src ?? []), 'file'],
  },
  // An unlisted tag is dropped but its children survive; <style> text must not.
  strip: [...(defaultSchema.strip ?? []), 'style'],
};
