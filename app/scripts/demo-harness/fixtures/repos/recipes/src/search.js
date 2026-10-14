import { recipes } from './recipes.js';

export function byName(query) {
  const needle = query.toLowerCase();
  return recipes.filter((recipe) => recipe.name.toLowerCase().includes(needle));
}
