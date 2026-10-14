import { turn } from '../turns.mjs';

export default {
  description: 'The guide screenshots: home screen, the Ledger button, the Worktrees tab',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion', 'tidy-ingredient-names'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt('Add a search that finds recipes by ingredient', turn()
      .title('Search by ingredient')
      .say("I'll start with how recipes are searched today.")
      .read('src/search.js')
      .say('Adding a byIngredient search next to byName.')
      .edit('src/search.js', {
        from: '  return recipes.filter((recipe) => recipe.name.toLowerCase().includes(needle));\n}',
        to: '  return recipes.filter((recipe) => recipe.name.toLowerCase().includes(needle));\n}\n\nexport function byIngredient(item) {\n  return recipes.filter((recipe) => recipe.ingredients.some((ingredient) => ingredient.item === item));\n}',
      })
      .ask('`byIngredient` matches exact names, so "egg" won\'t find "eggs". Should it match partial names too?'));

    const fix = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion', agent: 'codex' });
    await fix.prompt('Butter in cups fails to convert to grams, fix it', turn()
      .say('Checking the conversion table first.')
      .read('src/convert.js')
      .say('Butter and tablespoons are both missing; adding them and a test.')
      .hold());

    const repo = await demo.session({ repo: 'recipes' });
    await repo.prompt('Run the tests', turn()
      .run('npm test')
      .done('Both tests pass: flour converts at 120 g per cup and scaling doubles every ingredient.'));

    await demo.ui('dispatch_shortcut', { shortcutId: 'session.goToDashboard' });
    await demo.until('the GitHub rate limit banner to clear', () => demo.read('dom_wait', { selector: '.rate-limit-banner', absent: true, timeoutMs: 1000 }).then(() => true, () => false));
    await demo.pause();
    await demo.still('home-screen.png');
    await demo.still('ledger-button.png', {
      crop: { selector: '.sidebar-header', extendBottom: 72 },
      highlight: 'button[aria-label^="Open Ledger"]',
    });

    await demo.ui('worktrees_open_panel');
    await demo.ui('worktrees_refresh');
    await demo.until('the three worktrees in the ledger', async () => {
      const { rows = [] } = await demo.read('worktrees_get_state');
      return rows.length === 3 && rows.every((row) => !row.refreshing);
    });
    await demo.ui('dom_click', { selector: '.ledger-row[data-row-key$="recipes--fix-unit-conversion"]' });
    await demo.pause();
    await demo.still('ledger-worktrees.png');
  },
};
