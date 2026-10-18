import { turn } from '../turns.mjs';

export default {
  description: 'scratch: hold a world open for manual exploration',
  repos: { recipes: { worktrees: ['search-by-ingredient', 'fix-unit-conversion'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await search.prompt('Add a search that finds recipes by ingredient', turn()
      .title('Search by ingredient')
      .say("I'll start with how recipes are searched today.")
      .read('src/search.js')
      .ask('Should it match partial names too?'));
    console.log('[explore] ready');
    await demo.pause(45 * 60 * 1000);
  },
};
