import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { z } from 'astro/zod';

const guide = defineCollection({
  loader: glob({
    base: '../docs/guide',
    pattern: ['README.md', '{tutorials,reference}/*.md'],
    generateId: ({ entry }) => entry.split('/').pop()!.replace(/\.md$/, ''),
  }),
});

const features = defineCollection({
  loader: glob({ base: './src/content/features', pattern: '*.md' }),
  schema: z.object({
    title: z.string(),
    summary: z.string(),
    order: z.number(),
    guide: z.array(z.object({ link: z.string(), label: z.string() })).min(1),
  }),
});

export const collections = { guide, features };
