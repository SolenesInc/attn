import { VentSend, type Seed, type Vent } from '../../types/generated';

export const VIEWS = ['vents', 'all', 'bus', 'app', 'cli', 'sample'] as const;
export type View = (typeof VIEWS)[number];
export const VIEW_LABEL: Record<View, string> = { vents: 'Vents', all: 'All records', bus: 'bus', app: 'app', cli: 'cli', sample: 'sample' };

export interface StoredRecord {
  id: number;
  raw: string;
  stream: string;
  seq: number;
  name: string;
  at: string;
}

export type RecordItem =
  | { kind: 'record'; key: string; record: StoredRecord }
  | { kind: 'burst'; key: string; records: StoredRecord[] };
export type BurstItem = Extract<RecordItem, { kind: 'burst' }>;
export type VentItem = { kind: 'vent'; key: string; vent: Vent; done: boolean };
export type ListItem = VentItem | RecordItem;

export interface VentDestination {
  label: string;
  note: string;
  leaving: boolean;
  movable: boolean;
}

export function ventDestination(vent: Vent, sharing: boolean): VentDestination {
  const withdrawn = Boolean(vent.withdrawn_at);
  const sent = vent.send === VentSend.Sent;
  const queued = !withdrawn && vent.send === VentSend.Queued;
  if (sent) return { label: "Sent to attn's maintainers", note: '', leaving: true, movable: false };
  if (queued) return { label: sharing ? 'Sending with the next batch' : 'Sending once sharing is on', note: '', leaving: true, movable: true };
  return { label: 'Kept on this machine', note: withdrawn ? 'withdrawn' : sharing ? '' : 'sharing is off', leaving: false, movable: sharing && !withdrawn };
}

export const isDone = (vent: Vent) => Boolean(vent.dismissed_at || vent.withdrawn_at || vent.planted_seed_id);

export function parseRecord({ id, record }: { id: number; record: string }): StoredRecord {
  try {
    const parsed = JSON.parse(record) as Partial<StoredRecord>;
    return { id, raw: record, stream: String(parsed.stream ?? ''), seq: Number(parsed.seq ?? 0), name: String(parsed.name ?? ''), at: String(parsed.at ?? '') };
  } catch {
    return { id, raw: record, stream: '', seq: 0, name: '', at: '' };
  }
}

const sameSecond = (a: StoredRecord, b: StoredRecord) => a.name === b.name && a.at.slice(0, 19) === b.at.slice(0, 19);

export function recordItems(records: StoredRecord[], openBursts: ReadonlySet<string>): RecordItem[] {
  const items: RecordItem[] = [];
  for (let i = 0; i < records.length; i++) {
    let j = i;
    while (j + 1 < records.length && sameSecond(records[j + 1], records[i])) j++;
    const run = records.slice(i, j + 1);
    const key = `burst-${records[i].id}`;
    if (run.length > 1 && !openBursts.has(key)) items.push({ kind: 'burst', key, records: run });
    else items.push(...run.map((record): RecordItem => ({ kind: 'record', key: `record-${record.id}`, record })));
    i = j;
  }
  return items;
}

export function ventItems(vents: Vent[], seeds: ReadonlyMap<string, Seed>): VentItem[] {
  const doneAt = (vent: Vent) => vent.withdrawn_at || vent.dismissed_at || seeds.get(vent.planted_seed_id ?? '')?.created_at || vent.created_at;
  const fresh = vents.filter((vent) => !isDone(vent));
  const done = vents.filter(isDone).sort((a, b) => Date.parse(doneAt(b)) - Date.parse(doneAt(a)));
  return [...fresh, ...done].map((vent) => ({ kind: 'vent', key: vent.id, vent, done: isDone(vent) }));
}

export function nextNewVent(items: ListItem[], after: number): VentItem | undefined {
  return items.slice(after + 1).find((item): item is VentItem => item.kind === 'vent' && !item.done);
}
