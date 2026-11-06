import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';

const guide = defineCollection({
  loader: glob({
    base: '../docs/guide',
    pattern: ['README.md', '{tutorials,reference}/*.md'],
    generateId: ({ entry }) => entry.split('/').pop()!.replace(/\.md$/, ''),
  }),
});

export const collections = { guide };
