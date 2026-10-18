import { cached } from './cache.js';

const SUPPLIER = 'https://supplier.example.com';

async function fromSupplier(path) {
  const response = await fetch(`${SUPPLIER}${path}`);
  if (!response.ok) throw new Error(`Supplier answered ${response.status} for ${path}`);
  return response.json();
}

export function priceOf(sku) {
  return cached(`price:${sku}`, () => fromSupplier(`/prices/${sku}`));
}

export function stockOf(sku) {
  return cached(`stock:${sku}`, () => fromSupplier(`/stock/${sku}`));
}
