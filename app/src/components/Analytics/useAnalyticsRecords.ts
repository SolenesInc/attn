import { useCallback, useEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { parseRecord, type StoredRecord, type View } from './analyticsItems';

const PAGE_SIZE = 200;

export interface RecordsState {
  view: View;
  records: StoredRecord[];
  more: boolean;
  loading: boolean;
  error: string | null;
}

export function useAnalyticsRecords(view: View) {
  const { sendListAnalyticsRecords } = useDaemonApi();
  const [state, setState] = useState<RecordsState | null>(null);
  const loadSeq = useRef(0);

  const load = useCallback(async (before: StoredRecord[]) => {
    if (view === 'vents') return;
    const seq = ++loadSeq.current;
    setState({ view, records: before, more: false, loading: true, error: null });
    try {
      const page = await sendListAnalyticsRecords(view === 'all' ? null : view, before[before.length - 1]?.id ?? null, PAGE_SIZE);
      if (loadSeq.current !== seq) return;
      setState({ view, records: [...before, ...page.records.map(parseRecord)], more: page.records.length === PAGE_SIZE, loading: false, error: null });
    } catch (err) {
      if (loadSeq.current !== seq) return;
      setState({ view, records: before, more: before.length > 0, loading: false, error: err instanceof Error ? err.message : String(err) });
    }
  }, [view, sendListAnalyticsRecords]);

  useEffect(() => {
    void load([]);
  }, [load]);

  const current = state?.view === view ? state : null;
  return {
    records: current,
    older: () => current && void load(current.records),
    reload: () => load([]),
  };
}
