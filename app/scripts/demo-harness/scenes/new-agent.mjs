import { SEARCH_PROMPT, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'Cmd+N opens the picker, you pick a harness and a worktree, and the agent opens beside the one you are on',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());
    await demo.ui('dom_click', { selector: 'button[aria-label="Close chief setup"]' });
    await demo.clip('new-agent', async () => {
      demo.caption('`Cmd+N` (`Ctrl+Shift+N` on Linux) starts an agent');
      await demo.pause(1500);
      await demo.press('session.new');
      await demo.pause(400);
      await demo.focus('.location-picker');
      await demo.pause(1500);
      demo.caption('Pick a harness and a directory');
      await demo.key('return');
      await demo.pause(1000);
      await demo.focus('.location-picker');
      await demo.pause(800);
      await demo.key('arrowdown');
      await demo.pause(500);
      await demo.key('arrowdown');
      await demo.pause(900);
      await demo.key('return');
      await demo.focus(null);
      demo.caption("It opens beside the agent you're looking at");
      await demo.until('the new agent in the sidebar', async () => (await demo.read('get_state')).sessions.length === 2);
      await demo.pause(4000);
    });
  },
};
