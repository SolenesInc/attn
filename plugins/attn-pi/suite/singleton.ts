// pi's `/reload` clears the extension module cache (0.83.0), so the suite entrypoint is
// evaluated again in the same process: anything holding an OS resource keys on the process.
export function processSingleton<T>(key: string, build: () => T): T {
  const slot = Symbol.for(key);
  const host = globalThis as unknown as Record<symbol, unknown>;
  const existing = host[slot];
  if (existing !== undefined) return existing as T;
  const created = build();
  host[slot] = created;
  return created;
}
