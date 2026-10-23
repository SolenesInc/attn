import { SEARCH_PROMPT, searchByIngredient } from '../recipeTurns.mjs';

export default {
  description: 'A short clip: a Claude session reads, edits and asks a question, and the sidebar goes from working to waiting',
  repos: { recipes: { worktrees: ['search-by-ingredient'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await demo.clip('agent-turn', async () => {
      await demo.pause(1200);
      await search.prompt(SEARCH_PROMPT, searchByIngredient());
      await demo.pause(3000);
    });
  },
};
