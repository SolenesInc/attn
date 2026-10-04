import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { UserMessageClient, UserMessageItem } from './client';

export function useUserMessageHistory(client: UserMessageClient | undefined, open: boolean, connected: boolean, revision = 0) {
  const [history, setHistory] = useState<UserMessageItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const current = useRef({ client, open, connected, history });
  useLayoutEffect(() => { current.current = { client, open, connected, history }; });
  const opened = useRef(false);
  const generation = useRef(0);
  const pending = useRef<Promise<void> | null>(null);
  const refreshWanted = useRef(false);

  function refresh(cursor?: string): Promise<void> {
    if (pending.current) {
      if (!cursor) refreshWanted.current = true;
      return pending.current;
    }
    const api = current.current.client!;
    const epoch = generation.current;
    const oldest = cursor ? undefined : current.current.history[current.current.history.length - 1]?.id;
    setLoading(true);
    const operation = (async () => {
      try {
        let page = await api.recent(cursor);
        const items = [...page.items];
        while (epoch === generation.current && !cursor && oldest && !items.some(item => item.id === oldest) && page.nextCursor) {
          page = await api.recent(page.nextCursor);
          items.push(...page.items);
        }
        if (epoch !== generation.current) return;
        const previous = current.current.history;
        const updated = cursor ? [...previous, ...items.filter(item => !previous.some(saved => saved.id === item.id))] : items;
        current.current.history = updated; setHistory(updated);
        setNextCursor(page.nextCursor); setError('');
      } catch (error) { if (epoch === generation.current) setError(String(error)); }
      finally { if (epoch === generation.current) setLoading(false); }
    })();
    pending.current = operation;
    return operation.finally(() => {
      if (pending.current === operation) pending.current = null;
      if (refreshWanted.current && current.current.open && current.current.connected) {
        refreshWanted.current = false;
        return refresh();
      }
    });
  }

  useLayoutEffect(() => {
    if (!open) {
      if (opened.current) { generation.current++; opened.current = false; refreshWanted.current = false; }
      return;
    }
    if (!opened.current) {
      generation.current++; opened.current = true;
      current.current.history = [];
      setHistory([]); setNextCursor(undefined); setError('');
    }
    if (client && connected) void refresh();
  }, [client, open, connected, revision]);
  useEffect(() => () => { generation.current++; }, []);
  return { history, nextCursor, loading, error, refresh };
}
