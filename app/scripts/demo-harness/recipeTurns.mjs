import { turn } from './turns.mjs';

export const SEARCH_PROMPT = 'Add a search that finds recipes by ingredient';

export function searchByIngredient() {
  return turn()
    .title('Search by ingredient')
    .say("I'll start with how recipes are searched today.")
    .read('src/search.js')
    .say('Adding a byIngredient search next to byName.')
    .edit('src/search.js', {
      from: '  return recipes.filter((recipe) => recipe.name.toLowerCase().includes(needle));\n}',
      to: '  return recipes.filter((recipe) => recipe.name.toLowerCase().includes(needle));\n}\n\nexport function byIngredient(item) {\n  return recipes.filter((recipe) => recipe.ingredients.some((ingredient) => ingredient.item === item));\n}',
    })
    .ask('`byIngredient` matches exact names, so "egg" won\'t find "eggs". Should it match partial names too?');
}

export const BUTTER_PROMPT = 'Butter in cups fails to convert to grams, fix it';

export function fixButterConversion() {
  return turn()
    .say('Checking the conversion table first.')
    .read('src/convert.js')
    .say('Butter and tablespoons are both missing; adding them and a test.')
    .edit('src/convert.js', {
      from: '  stock: 240,\n};',
      to: '  stock: 240,\n  butter: 227,\n};',
    });
}
