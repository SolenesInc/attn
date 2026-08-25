
import type {
  BusConsumerStatus,
  BusHealthEntry,
  BusProducerStatus,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

export type { BusConsumerStatus, BusHealthEntry, BusProducerStatus };

export interface BusStatus {
  earliest: number;
  head: number;
  rows: number;
  bytes: number;
  /** RFC3339; empty on an empty log. */
  oldestAt: string;
  newestAt: string;
  delivering: boolean;
  retentionSeconds: number;
  recentWindowSeconds: number;
  baselineWindowSeconds: number;
  surgeRatePerHour: number;
  pinAlarmSeconds: number;
  producers: BusProducerStatus[];
  consumers: BusConsumerStatus[];
  health: BusHealthEntry[];
}

interface BusDaemonEvent {
  event?: string;
  success?: boolean;
  error?: string;
  [key: string]: unknown;
}

const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const list = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);

const toBusStatus = (event: BusDaemonEvent): BusStatus => ({
  earliest: num(event.earliest),
  head: num(event.head),
  rows: num(event.rows),
  bytes: num(event.bytes),
  oldestAt: str(event.oldest_at),
  newestAt: str(event.newest_at),
  delivering: event.delivering === true,
  retentionSeconds: num(event.retention_seconds),
  recentWindowSeconds: num(event.recent_window_seconds),
  baselineWindowSeconds: num(event.baseline_window_seconds),
  surgeRatePerHour: num(event.surge_rate_per_hour),
  pinAlarmSeconds: num(event.pin_alarm_seconds),
  producers: list<BusProducerStatus>(event.producers),
  consumers: list<BusConsumerStatus>(event.consumers),
  health: list<BusHealthEntry>(event.health),
});

export function handleBusDaemonEvent(event: BusDaemonEvent, pending: PendingRequests): boolean {
  switch (event.event) {
    case 'bus_status_result':
      settlePendingRequest(
        pending,
        'bus_status_get',
        event,
        toBusStatus,
        'Reading the event bus failed',
      );
      return true;
    case 'bus_set_consumer_enabled_result':
      settlePendingRequest(
        pending,
        'bus_set_consumer_enabled',
        event,
        (settled): { consumer: string } => ({ consumer: str(settled.consumer) }),
        'Changing the consumer failed',
      );
      return true;
    default:
      return false;
  }
}
