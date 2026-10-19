import { SEARCH_PROMPT, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'Cmd+D splits a shell beside the agent and Cmd+Shift+D splits one below',
  repos: { recipes: { worktrees: ['search-by-ingredient'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt(SEARCH_PROMPT, searchByIngredient());
    await demo.clip('split-terminals', async () => {
      demo.caption('`Cmd+D` (`Ctrl+Shift+D` on Linux) opens a shell beside the agent');
      await demo.pause(1500);
      await demo.press('terminal.splitVertical');
      await demo.pause(1500);
      await demo.type('npm test');
      await demo.key('return');
      await demo.pause(2500);
      demo.caption('`Cmd+Shift+D` (`Ctrl+Alt+D` on Linux) opens one below');
      await demo.press('terminal.splitHorizontal');
      await demo.pause(1500);
      await demo.type('git status --short');
      await demo.key('return');
      await demo.pause(3500);
    });
  },
};
