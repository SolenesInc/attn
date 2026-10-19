import { turn } from '../turns.mjs';
import { BUTTER_PROMPT, SEARCH_PROMPT, fixButterConversion, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'Queue flow: attn brings the agent that has waited longest, you answer and settle it, and attn brings the next one',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());
    const fix = await demo.session({ repo: 'recipes', worktree: 'fix-unit-conversion' });
    await fix.prompt(BUTTER_PROMPT, fixButterConversion().ask('Butter converts at 227 g per cup now. Should I add tablespoons too?'));
    await demo.press('desktop.sendStay2');
    await demo.ui('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    await demo.press('session.goToDashboard');
    await demo.read('dom_wait', { selector: '[data-testid="queue-waiting-card"][data-waiting="2"]', timeoutMs: 10_000 });
    await demo.waitOutGitHubThrottle();

    const onScreen = async (sessionId) => (await demo.read('get_state')).activeLeaf?.sessionId === sessionId;
    await demo.clip('queue-loop', async () => {
      demo.caption('`Cmd+J` (`Ctrl+Shift+J` on Linux) takes you to the agent that has waited longest');
      await demo.pause(1500);
      await demo.press('session.jumpToWaiting');
      await demo.until('the oldest turn on screen', () => onScreen(search.id));
      await demo.pause(1000);
      demo.caption('You answer it');
      await search.prompt('Yes, match partial names too', turn()
        .say('Matching partial names now.')
        .read('src/search.js')
        .hold());
      await demo.pause(800);
      demo.caption('`Cmd+Shift+E` (`Ctrl+Alt+E` on Linux) settles it, and Attn brings the next one');
      await demo.pause(1200);
      await demo.press('session.settle');
      await demo.until('the next turn on screen', () => onScreen(fix.id));
      await demo.pause(1200);
      await fix.prompt('Yes, add tablespoons', turn()
        .say('Adding tablespoons at 14 g each.')
        .read('src/convert.js')
        .hold());
      await demo.pause(800);
      await demo.press('session.settle');
      await demo.read('dom_wait', { selector: '[data-testid="all-settled"]', timeoutMs: 10_000 });
      demo.caption('When nobody is waiting, you land Home');
      await demo.focus('[data-testid="all-settled"]');
      await demo.pause(3000);
      await demo.focus(null);
      await demo.pause(1200);
    });
  },
};
