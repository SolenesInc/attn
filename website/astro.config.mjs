import { defineConfig } from 'astro/config';
import { rewriteGuideLinks } from './src/guide/links.mjs';

export default defineConfig({
  server: { port: 4321 },
  devToolbar: { enabled: false },
  markdown: {
    remarkPlugins: [rewriteGuideLinks],
    shikiConfig: {
      themes: { light: 'github-light', dark: 'github-dark' },
      defaultColor: false,
    },
  },
});
