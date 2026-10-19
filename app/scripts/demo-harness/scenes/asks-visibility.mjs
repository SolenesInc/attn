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

const PAGING_ASK = `attn ask --subject "When to page someone" <<'ASK'
A charge can fail for hours when the processor is degraded.

:::question
After how many failed retries should on-call be paged?
- [ ] 3 — about 20 minutes in
- [ ] 5 — about 2 hours in
- [ ] Never — log it and keep retrying
Recommended: 5
:::
ASK`;

const QUEUE_BAR = '[data-testid="queue-bar"]';
const ASKS_CHIP = '[data-testid="queue-bar-asks"]';
const ASKS_PEEK = '[data-testid="queue-bar-asks-peek"]';

const present = (demo, selector) => demo.read('dom_wait', { selector, timeoutMs: 1000 }).then(() => true, () => false);

export default {
  description: 'Stills: open asks in the sidebar header, on the queue bar and its peek, in the asking agent\'s tile header, and on the collapsed desktop rail',
  repos: { shop: { worktrees: ['persistent-cache', 'billing-retry'] } },
  async run(demo) {
    await demo.ui('set_setting', { key: 'asks_enabled', value: 'true' });
    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'true' });

    const cache = await demo.session({ repo: 'shop', worktree: 'persistent-cache' });
    await cache.prompt('Move the cache to a persistent store', turn()
      .title('Persistent cache')
      .say("It's a Map in memory, so every deploy starts it empty. Where it should live is your call, so I'll ask and keep going.")
      .run(CACHE_STORE_ASK)
      .say("While you decide, I'll look at what reads the cache.")
      .read('src/catalog.js')
      .hold());
    const billing = await demo.session({ repo: 'shop', worktree: 'billing-retry' });
    await billing.prompt('Retry failed charges with backoff', turn()
      .title('Billing retry')
      .say('Backoff is in. Paging is a policy call, so I asked you in attn.')
      .run(PAGING_ASK)
      .ask("I'll wire the alert once you answer. Anything else meanwhile?"));
    await demo.until('both agents marked as asking', async () => (
      await present(demo, `[data-testid="ask-marker-${cache.id}"]`) && present(demo, `[data-testid="ask-marker-${billing.id}"]`)
    ));

    if (await present(demo, QUEUE_BAR)) await demo.ui('dispatch_shortcut', { shortcutId: 'session.toggleSidebar' });
    await demo.read('dom_wait', { selector: '.sidebar-header-panels .sidebar-count-badge', textIncludes: '2', timeoutMs: 10_000 });
    await demo.still('sidebar-header-asks.png', { crop: { selector: '.sidebar-header' } });
    await demo.ui('dispatch_shortcut', { shortcutId: 'session.toggleSidebar' });
    await demo.read('dom_wait', { selector: ASKS_CHIP, textIncludes: '2 asks', timeoutMs: 10_000 });
    await demo.pause();
    await demo.still('queue-bar-asks.png', { crop: { selector: QUEUE_BAR } });
    await demo.ui('dom_hover', { selector: '[data-testid="queue-bar-asks-anchor"]' });
    await demo.read('dom_wait', { selector: ASKS_PEEK, timeoutMs: 10_000 });
    await demo.pause();
    await demo.still('queue-bar-asks-peek.png');
    await demo.ui('dom_hover', { selector: '[data-testid="queue-bar-asks-anchor"]', leave: true });

    const chip = `[data-testid="ask-chip-${cache.id}"]`;
    await demo.ui('select_session', { sessionId: cache.id });
    await demo.read('dom_wait', { selector: chip, timeoutMs: 10_000 });
    await demo.ui('dom_hover', { selector: chip });
    await demo.read('dom_wait', { selector: '.ask-preview', textIncludes: 'Which store should keep the cache?', timeoutMs: 10_000 });
    await demo.pause();
    await demo.still('tile-ask-preview.png');
    await demo.ui('dom_hover', { selector: chip, leave: true });
    await demo.ui('dom_click', { selector: chip });
    await demo.read('dom_wait', { selector: '.asks-ask-subject', textIncludes: 'Cache store', timeoutMs: 10_000 });
    await demo.pause();
    await demo.still('tile-ask-opens-drawer.png');
    await demo.ui('dispatch_shortcut', { shortcutId: 'dock.asks' });
    await demo.read('dom_wait', { selector: '.asks-drawer', absent: true, timeoutMs: 10_000 });

    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'false' });
    await demo.read('dom_wait', { selector: '.sidebar.collapsed .sidebar-count-badge', textIncludes: '2', timeoutMs: 10_000 });
    await demo.pause();
    await demo.still('rail-asks.png', { crop: { selector: '.sidebar.collapsed' } });
  },
};
