// Attaches to the same session must not overlap: the pending-action key, the attach
// context and the queued-output buffer are per-session singletons in the socket.
export function enqueuePerKey<T>(
  chains: Map<string, Promise<unknown>>,
  key: string,
  task: () => Promise<T>,
): Promise<T> {
  const prior = chains.get(key) ?? Promise.resolve();
  const next = prior.catch(() => {}).then(task);
  chains.set(key, next);
  void next.catch(() => {}).finally(() => {
    if (chains.get(key) === next) {
      chains.delete(key);
    }
  });
  return next;
}
