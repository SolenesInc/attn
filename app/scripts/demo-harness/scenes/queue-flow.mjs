import { turn } from '../turns.mjs';

const QUEUE = '[data-testid="sidebar-queue"]';
const ALL_SETTLED = '[data-testid="all-settled"]';
const WORKING_LIST = '[data-testid="session-group-working"]';

const ANSWERS = {
  search: 'None. An empty query returns nothing.',
  butter: 'Yes, 14 grams each.',
  cache: 'Yes, thirty seconds for stock.',
  retry: 'Three is right. Ship it.',
};

export default {
  description: 'Raw footage for the queue flow video: five agents, three waiting, answered and settled one after another, Home when nobody waits, and attn bringing the agent that asks next',
  window: { x: 80, y: 60, width: 960, height: 640 },
  repos: {
    recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion', 'scale-servings'] },
    shop: { worktrees: ['catalog-refresh', 'supplier-retry'] },
  },
  async run(demo) {
    const press = (shortcutId) => demo.ui('dispatch_shortcut', { shortcutId });
    const waiting = async () => (await demo.read('queue_get_state')).turns.map((row) => row.id);
    const onScreen = async (sessionId) => (await demo.read('queue_get_state')).turns.some((row) => row.id === sessionId && row.selected);
    const settleQuietly = async (session) => {
      await demo.ui('select_session', { sessionId: session.id });
      await press('session.settle');
      await demo.until(`${session.id} out of the queue`, async () => !(await waiting()).includes(session.id));
    };

    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'true' });

    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient', label: 'Search by ingredient' });
    await search.prompt('Add a search that finds recipes by ingredient', turn()
      .title('Search by ingredient')
      .say("I'll start with how recipes are searched today.")
      .read('src/search.js')
      .edit('src/search.js', {
        from: 'export function byName(query) {',
        to: 'export function byIngredient(query) {\n  const needle = query.toLowerCase();\n  return recipes.filter((recipe) => recipe.ingredients.some(({ item }) => item.includes(needle)));\n}\n\nexport function byName(query) {',
      })
      .ask('Search works. Should an empty query return every recipe, or none?'));

    const butter = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion', agent: 'codex', label: 'Fix butter conversion' });
    await butter.prompt('Butter in cups fails to convert to grams, fix it', turn()
      .title('Fix butter conversion')
      .say('Butter is missing from the table.')
      .read('src/convert.js')
      .edit('src/convert.js', { from: '  stock: 240,', to: '  stock: 240,\n  butter: 227,' })
      .ask('Butter converts at 227 g per cup now. Should I add tablespoons too?'));

    const cache = await demo.session({ repo: 'shop', worktree: 'catalog-refresh', label: 'Refresh the catalog cache' });
    await cache.prompt('Prices stay stale for ten minutes after a supplier update', turn()
      .title('Refresh the catalog cache')
      .say('The cache keeps prices for ten minutes.')
      .read('src/cache.js')
      .edit('src/cache.js', { from: 'const TTL_MS = 10 * 60 * 1000;', to: 'const TTL_MS = 60 * 1000;' })
      .ask('Prices now expire after a minute. Should stock counts expire sooner?'));

    let everyoneSettled;
    const retryMayAsk = new Promise((resolve) => { everyoneSettled = resolve; });
    const retry = await demo.session({ repo: 'shop', worktree: 'supplier-retry', agent: 'codex', label: 'Retry supplier errors' });
    await retry.prompt('The supplier API fails now and then, retry it', turn()
      .title('Retry supplier errors')
      .say('Requests fail on the first error.')
      .read('src/catalog.js')
      .edit('src/catalog.js', {
        from: 'async function fromSupplier(path) {\n  const response = await fetch(`${SUPPLIER}${path}`);',
        to: 'async function fromSupplier(path, attempt = 1) {\n  const response = await fetch(`${SUPPLIER}${path}`);\n  if (response.status >= 500 && attempt < 3) return fromSupplier(path, attempt + 1);',
      })
      .after(retryMayAsk)
      .ask('Retries are in. Three tries before it gives up?'));

    const servings = await demo.session({ repo: 'recipes', worktree: 'scale-servings', label: 'Scale servings' });
    await servings.prompt('Let people scale a recipe to any number of servings', turn()
      .title('Scale servings')
      .say('Reading how a recipe is scaled today.')
      .read('src/convert.js')
      .hold());

    await settleQuietly(retry);
    await settleQuietly(servings);
    await press('session.goToDashboard');
    await demo.until('three agents waiting', async () => (await waiting()).length === 3);

    const answer = async (session, question, text, builder) => {
      await session.markText('question', question);
      await demo.pause(3500);
      await session.prompt(text, builder);
      await demo.pause(2500);
    };
    const settle = async (session) => {
      await demo.mark('settle', QUEUE);
      await press('session.settle');
      await demo.until(`${session.id} out of the queue`, async () => !(await waiting()).includes(session.id));
      await demo.pause(1500);
    };

    await demo.clip('queue-flow', async () => {
      await demo.mark('queue', QUEUE);
      await demo.mark('oldest-waiting', `[data-testid="queue-turn-${search.id}"]`);
      await demo.mark('newest-waiting', `[data-testid="queue-turn-${cache.id}"]`);
      await demo.pause(6000);

      await demo.mark('jump', QUEUE);
      await press('session.jumpToWaiting');
      await demo.until('the agent that waited longest on screen', () => onScreen(search.id));
      await answer(search, 'Search works.', ANSWERS.search, turn().say('Returning nothing for an empty query.').read('src/search.js').hold());
      await settle(search);

      await demo.until('the next agent on screen', () => onScreen(butter.id));
      await answer(butter, 'Butter converts', ANSWERS.butter, turn().say('Adding tablespoons at 14 g each.').read('src/convert.js').hold());
      await settle(butter);

      await demo.until('the last waiting agent on screen', () => onScreen(cache.id));
      await answer(cache, 'Prices now expire', ANSWERS.cache, turn().say('Stock gets its own thirty second expiry.').read('src/cache.js').hold());
      await settle(cache);

      await demo.read('dom_wait', { selector: ALL_SETTLED, timeoutMs: 10_000 });
      await demo.mark('all-settled', ALL_SETTLED);
      await demo.mark('working-list', WORKING_LIST);
      await demo.pause(9000);

      everyoneSettled();
      await demo.until('attn bringing the agent that asked', () => onScreen(retry.id));
      await demo.mark('brought', `[data-testid="queue-turn-${retry.id}"]`);
      await answer(retry, 'Retries are in.', ANSWERS.retry, turn().say('Three tries it is.').read('src/catalog.js').hold());
      await settle(retry);
      await demo.read('dom_wait', { selector: ALL_SETTLED, timeoutMs: 10_000 });
      await demo.mark('all-settled-again', ALL_SETTLED);
      await demo.pause(6000);
    });
  },
};
