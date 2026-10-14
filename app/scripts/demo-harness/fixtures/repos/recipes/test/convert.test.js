import assert from 'node:assert/strict';
import { test } from 'node:test';
import { scale, toGrams } from '../src/convert.js';
import { recipes } from '../src/recipes.js';

test('a cup of flour weighs 120g', () => {
  assert.equal(toGrams({ item: 'flour', amount: 1, unit: 'cup' }), 120);
});

test('scaling doubles every ingredient', () => {
  const doubled = scale(recipes[0], 4);
  assert.equal(doubled.ingredients[0].amount, 8);
});
