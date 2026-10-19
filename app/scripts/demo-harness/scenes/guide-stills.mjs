import { turn } from '../turns.mjs';
import { BUTTER_PROMPT, SEARCH_PROMPT, fixButterConversion, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'The guide screenshots: home screen, the Ledger button, the Worktrees tab',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion', 'tidy-ingredient-names'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());

    const fix = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion', agent: 'codex' });
    await fix.prompt(BUTTER_PROMPT, fixButterConversion().hold());

    const repo = await demo.session({ repo: 'recipes' });
    await repo.prompt('Run the tests', turn()
      .run('npm test')
      .done('Both tests pass: flour converts at 120 g per cup and scaling doubles every ingredient.'));

    await demo.ui('dispatch_shortcut', { shortcutId: 'session.goToDashboard' });
    await demo.waitOutGitHubThrottle();
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
