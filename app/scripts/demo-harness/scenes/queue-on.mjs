import { turn } from '../turns.mjs';
import { BUTTER_PROMPT, SEARCH_PROMPT, fixButterConversion, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'The command palette turns on the agent queue, and the sidebar puts the agents waiting for you first',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());
    const tests = await demo.session({ repo: 'recipes' });
    await tests.prompt('Run the tests', turn()
      .run('npm test')
      .done('Both tests pass: flour converts at 120 g per cup and scaling doubles every ingredient.'));
    await demo.press('desktop.sendStay2');
    const fix = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion' });
    await fix.prompt(BUTTER_PROMPT, fixButterConversion().hold());
    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await demo.read('dom_wait', { selector: `[data-testid="queue-settle-${fix.id}"]`, timeoutMs: 10_000 });
    await demo.ui('dom_click', { selector: `[data-testid="queue-settle-${fix.id}"]` });
    await demo.until('the working agent to leave the turns', async () => {
      const { turns = [] } = await demo.read('queue_get_state');
      return !turns.some((row) => row.id === fix.id);
    });
    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'false' });

    await demo.clip('queue-on', async () => {
      demo.caption('Open the command palette with `Cmd+Shift+K` (`Ctrl+Alt+K` on Linux)');
      await demo.pause(1500);
      await demo.press('ui.commandPalette');
      await demo.pause(300);
      await demo.focus('.palette-box');
      await demo.pause(600);
      demo.caption('Run "Turn on the agent queue"');
      await demo.type('queue');
      await demo.pause(1200);
      await demo.key('return');
      await demo.read('dom_wait', { selector: '[data-testid="queue-waiting-card"]', timeoutMs: 10_000 });
      await demo.focus('[data-testid="queue-waiting-card"]');
      demo.caption('The agents waiting for you come first, oldest on top');
      await demo.pause(3500);
      await demo.focus(null);
      await demo.pause(1500);
    });
  },
};
