import { cached } from './cache.js';

const SUPPLIER = 'https://supplier.example.com';

async function fromSupplier(path) {
  const response = await fetch(`${SUPPLIER}${path}`);
  return response.json();
}

export function priceOf(sku) {
  return cached(`price:${sku}`, () => fromSupplier(`/prices/${sku}`));
}

export function stockOf(sku) {
  return cached(`stock:${sku}`, () => fromSupplier(`/stock/${sku}`));
}
