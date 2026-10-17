import { turn } from '../turns.mjs';

const CACHE_STORE_ASK = `attn ask --subject "Cache store" <<'ASK'
Deploys empty the cache, so the first requests after one are slow.

:::question
Which store should keep the cache?
Postgres runs already; Redis is faster but adds a service to operate.
- [ ] Postgres — already deployed
- [ ] Redis — faster, one more service
Recommended: Postgres
:::
ASK`;

const POSTGRES = '.asks-ask .ask-choice:first-of-type';
const NOTE = '.asks-note';

export default {
  description: 'A clip and a still: a Claude session asks which store should keep the cache and keeps working, and the answer goes back from the Asks drawer',
  repos: { shop: { worktrees: ['persistent-cache'] } },
  async run(demo) {
    await demo.ui('set_setting', { key: 'asks_enabled', value: 'true' });
    const cache = await demo.session({ repo: 'shop', worktree: 'persistent-cache' });
    const marker = `[data-testid="ask-marker-${cache.id}"]`;
    await demo.clip('asks', async () => {
      await demo.pause(1200);
      await cache.prompt('Move the cache to a persistent store', turn()
        .title('Persistent cache')
        .say("I'll start with how the cache works today.")
        .read('src/cache.js')
        .say("It's a Map in memory, so every deploy starts it empty. Where it should live is your call, so I'll ask and keep going.")
        .run(CACHE_STORE_ASK)
        .say("While you decide, I'll look at what reads the cache.")
        .read('src/catalog.js')
        .hold());
      await demo.until('the ask marker on the agent', () => demo.read('dom_wait', { selector: marker, timeoutMs: 1000 }).then(() => true, () => false));
      await demo.pause(3000);

      await demo.ui('dispatch_shortcut', { shortcutId: 'dock.asks' });
      await demo.read('dom_wait', { selector: POSTGRES, textIncludes: 'Postgres', timeoutMs: 10_000 });
      await demo.pause();
      await demo.still('asks-drawer.png', { crop: { selector: '.dock-panel--asks' } });
      await demo.pause(4000);

      await demo.ui('dom_click', { selector: `${POSTGRES} input` });
      await demo.pause(800);
      const note = 'Keep it boring.';
      await demo.ui('dom_focus', { selector: NOTE });
      for (let typed = 1; typed <= note.length; typed++) {
        await demo.read('dom_type', { selector: NOTE, text: note.slice(0, typed) });
        await demo.pause(45);
      }
      await demo.pause();
      await demo.ui('dom_key', { selector: NOTE, key: 'Enter', modifiers: { meta: true } });
      await demo.read('dom_wait', { selector: '.asks-empty', timeoutMs: 10_000 });
      await demo.read('dom_wait', { selector: marker, absent: true, timeoutMs: 10_000 });
      await demo.pause(2000);

      await demo.ui('dispatch_shortcut', { shortcutId: 'dock.asks' });
      await demo.read('dom_wait', { selector: '.asks-drawer', absent: true, timeoutMs: 10_000 });
      await demo.pause(3000);
    });
  },
};
