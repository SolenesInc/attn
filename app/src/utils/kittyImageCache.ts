// Two keys, as the wire has two: an ENTRY is keyed (session, image id, generation), a
// REQUEST only (session, image id) — get_kitty_image carries no generation.

import {
  kittyPixelFormatFromName,
  type KittyPixelFormat,
} from './kittyImageFormat';

export interface KittyImageBlob {
  sessionId: string;
  imageId: number;
  generation: number;
  width: number;
  height: number;
  format: KittyPixelFormat;
  pixels: Uint8Array;
}

export type KittyImageStatus = 'present' | 'pending' | 'failed' | 'absent';

export type KittyImageRequestSender = (sessionId: string, imageId: number) => boolean;

export type KittyImageListener = (sessionId: string, imageId: number) => void;

// Measured 2026-08-03 (kitten icat, timg, chafa): largest stored image 6.48MB of decoded
// pixels, typical 1.9-6MB. 64MB is ~10x the worst single image — a tripwire, not a budget.
export const KITTY_BLOB_CACHE_BYTES = 64 * 1024 * 1024;

interface CacheEntry {
  // null marks a key the daemon could not serve.
  blob: KittyImageBlob | null;
  bytes: number;
}

// The free-form session id goes LAST, so no separator inside it can collide.
const entryKey = (sessionId: string, imageId: number, generation: number) =>
  `${imageId}:${generation}:${sessionId}`;

const requestKey = (sessionId: string, imageId: number) => `${imageId}:${sessionId}`;

export class KittyImageCache {
  // Insertion order is LRU order; a present entry is re-inserted on read.
  private readonly entries = new Map<string, CacheEntry>();
  private readonly inFlight = new Map<string, Set<number>>();
  private readonly listeners = new Set<KittyImageListener>();
  private sender: KittyImageRequestSender | null = null;
  private totalBytes = 0;

  constructor(private readonly capacityBytes: number = KITTY_BLOB_CACHE_BYTES) {}

  setSender(sender: KittyImageRequestSender | null): void {
    this.sender = sender;
  }

  subscribe(listener: KittyImageListener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  status(sessionId: string, imageId: number, generation: number): KittyImageStatus {
    const entry = this.entries.get(entryKey(sessionId, imageId, generation));
    if (entry) return entry.blob ? 'present' : 'failed';
    if (this.inFlight.get(requestKey(sessionId, imageId))?.has(generation)) return 'pending';
    return 'absent';
  }

  get(sessionId: string, imageId: number, generation: number): KittyImageBlob | null {
    const key = entryKey(sessionId, imageId, generation);
    const entry = this.entries.get(key);
    if (!entry?.blob) return null;
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.blob;
  }

  // A send the socket refuses leaves the key absent, so the next description tries
  // again.
  ensure(sessionId: string, imageId: number, generation: number): void {
    if (this.status(sessionId, imageId, generation) !== 'absent') return;
    const request = requestKey(sessionId, imageId);
    const waiting = this.inFlight.get(request);
    if (waiting) {
      waiting.add(generation);
      return;
    }
    if (!this.sender || !this.sender(sessionId, imageId)) return;
    this.inFlight.set(request, new Set([generation]));
  }

  fill(blob: KittyImageBlob): void {
    this.inFlight.delete(requestKey(blob.sessionId, blob.imageId));
    const key = entryKey(blob.sessionId, blob.imageId, blob.generation);
    this.drop(key);
    const bytes = blob.pixels.byteLength;
    this.evictFor(bytes, blob);
    this.entries.set(key, { blob, bytes });
    this.totalBytes += bytes;
    this.notify(blob.sessionId, blob.imageId);
  }

  // The answer names no generation, so every generation waiting on the request is
  // marked and stops being asked.
  markFailed(sessionId: string, imageId: number, reason: string): void {
    const request = requestKey(sessionId, imageId);
    const waiting = this.inFlight.get(request);
    this.inFlight.delete(request);
    console.warn(
      `[kitty] image ${imageId} of session ${sessionId} has no pixels: ${reason}`,
    );
    for (const generation of waiting ?? []) {
      const key = entryKey(sessionId, imageId, generation);
      this.drop(key);
      this.entries.set(key, { blob: null, bytes: 0 });
    }
    this.notify(sessionId, imageId);
  }

  bytes(): number {
    return this.totalBytes;
  }

  private drop(key: string): void {
    const existing = this.entries.get(key);
    if (!existing) return;
    this.entries.delete(key);
    this.totalBytes -= existing.bytes;
  }

  // An image larger than the whole cache is stored anyway and says so: the limit
  // needs remeasuring, not the image dropped.
  private evictFor(bytes: number, incoming: KittyImageBlob): void {
    while (this.totalBytes + bytes > this.capacityBytes && this.entries.size > 0) {
      const held = this.totalBytes;
      const oldest = this.entries.keys().next().value as string;
      const evicted = this.entries.get(oldest);
      this.entries.delete(oldest);
      this.totalBytes -= evicted?.bytes ?? 0;
      console.warn(
        `[kitty] blob cache limit ${this.capacityBytes} bytes reached (holding ${held}, asked for ${bytes} by image ${incoming.imageId} of session ${incoming.sessionId}) — evicted ${evicted?.bytes ?? 0} bytes`,
      );
    }
    if (bytes > this.capacityBytes) {
      console.warn(
        `[kitty] image ${incoming.imageId} of session ${incoming.sessionId} is ${bytes} bytes, past the whole ${this.capacityBytes}-byte blob cache limit — kept anyway; the limit needs remeasuring`,
      );
    }
  }

  private notify(sessionId: string, imageId: number): void {
    for (const listener of this.listeners) {
      listener(sessionId, imageId);
    }
  }
}

export const kittyImageCache = new KittyImageCache();

export interface KittyImageResultLike {
  id?: string;
  image_id?: number;
  success?: boolean;
  generation?: number;
  width?: number;
  height?: number;
  format?: string;
  data_b64?: string;
}

export function kittyImageBlobFromResult(result: KittyImageResultLike): KittyImageBlob | null {
  if (!result.success || !result.id || typeof result.image_id !== 'number') return null;
  const format = typeof result.format === 'string' ? kittyPixelFormatFromName(result.format) : null;
  if (
    !format
    || typeof result.generation !== 'number'
    || typeof result.width !== 'number'
    || typeof result.height !== 'number'
    || typeof result.data_b64 !== 'string'
  ) {
    return null;
  }
  const binary = atob(result.data_b64);
  const pixels = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) pixels[i] = binary.charCodeAt(i);
  return {
    sessionId: result.id,
    imageId: result.image_id,
    generation: result.generation,
    width: result.width,
    height: result.height,
    format,
    pixels,
  };
}
