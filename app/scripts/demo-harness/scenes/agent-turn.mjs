import { turn } from '../turns.mjs';

export default {
  description: 'A short clip: a Claude session reads, edits and asks a question, and the sidebar goes from working to waiting',
  repos: { recipes: { worktrees: ['search-by-ingredient'] } },
  async run(demo) {
    const search = await demo.session({ repo: 'recipes', worktree: 'search-by-ingredient' });
    await demo.clip('agent-turn', async () => {
      await demo.pause(1200);
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
      await demo.pause(3000);
    });
  },
};
