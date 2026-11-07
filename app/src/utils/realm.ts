type GlobalScope = typeof globalThis;

export type DomInterfaceName = {
  [K in keyof GlobalScope]: GlobalScope[K] extends { prototype: object; new (...args: never[]): object } ? K : never;
}[keyof GlobalScope];

export type DomInstance<K extends DomInterfaceName> = GlobalScope[K] extends { prototype: infer P } ? P : never;

export function isDomInstance<K extends DomInterfaceName>(value: unknown, name: K): value is DomInstance<K> {
  if (typeof value !== 'object' || value === null) return false;
  for (let prototype = Object.getPrototypeOf(value); prototype !== null; prototype = Object.getPrototypeOf(prototype)) {
    if (Object.prototype.hasOwnProperty.call(prototype, 'constructor') && prototype.constructor.name === name) return true;
  }
  return false;
}

export function ownerWindow(node: Node): Window {
  const document = isDomInstance(node, 'Document') ? node : node.ownerDocument;
  return document?.defaultView ?? window;
}
