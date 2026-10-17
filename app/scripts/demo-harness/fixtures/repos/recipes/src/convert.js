const GRAMS_PER_CUP = {
  flour: 120,
  sugar: 200,
  milk: 240,
  stock: 240,
};

export function toGrams({ item, amount, unit }) {
  if (unit === 'g') return amount;
  if (unit === 'cup') return amount * GRAMS_PER_CUP[item];
  throw new Error(`cannot convert ${unit} of ${item} to grams`);
}

export function scale(recipe, serves) {
  const factor = serves / recipe.serves;
  return {
    ...recipe,
    serves,
    ingredients: recipe.ingredients.map((ingredient) => ({ ...ingredient, amount: ingredient.amount * factor })),
  };
}
