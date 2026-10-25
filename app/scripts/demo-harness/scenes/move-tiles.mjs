import { turn } from '../turns.mjs';
import { BUTTER_PROMPT, SEARCH_PROMPT, fixButterConversion, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'A tile dragged onto desktop 2 in the sidebar moves there, and Cmd+Option+1 brings it back',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion'] } },
  async run(demo) {
    const tests = await demo.session({ repo: 'recipes' });
    await tests.prompt('Run the tests', turn()
      .run('npm test')
      .done('Both tests pass: flour converts at 120 g per cup and scaling doubles every ingredient.'));
    await demo.press('desktop.sendStay2');

    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());
    const fix = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion' });
    await fix.prompt(BUTTER_PROMPT, fixButterConversion().ask('Butter converts at 227 g per cup now. Should I add tablespoons too?'));

    const { arrangement } = await demo.read('get_state');
    const desktop2 = arrangement.desktops.find((desktop) => desktop.slot === 2);
    const fixPane = arrangement.desktops.flatMap((desktop) => desktop.panes).find((pane) => pane.sessionId === fix.id);

    await demo.clip('move-tiles', async () => {
      demo.caption('Drag a tile onto a desktop in the sidebar');
      await demo.pause(1500);
      await demo.drag(`[data-pane-id="${fixPane.paneId}"] .desktop-pane-header`, `[data-testid="sidebar-desktop-${desktop2.id}"]`);
      await demo.pause(2500);
      demo.caption('`Cmd+Option+1` (`Ctrl+Alt+1` on Linux) moves it back and takes you with it');
      await demo.pause(800);
      await demo.press('desktop.send1');
      await demo.pause(3500);
    });
  },
};
