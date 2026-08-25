
import type { StoredDocument } from '../types/generated';

export interface DocumentRevision {
  id: string;
  rev: number;
}

// `after` is absent by construction: a live query is a window.
export interface DocumentQueryRequest {
  namespace: string;
  collection: string;
  filters?: Array<{ field: string; op: string; value: unknown }>;
  sort?: { field: string; desc?: boolean };
  limit?: number;
}

// The client rule is the whole contract: render `order`, take each body from `upsert` if
// it is there else from your cache, forget every cached document not named in `order`.
export interface DocumentDelivery {
  delivery: number;
  asOfSeq: number;
  order: string[];
  upsert: StoredDocument[];
}

export interface DocumentSubscriber {
  request: DocumentQueryRequest;
  have: () => DocumentRevision[];
  onDelivery: (delivery: DocumentDelivery) => void;
  onEnded: (code: string, message: string) => void;
  onLive: (live: boolean) => void;
}

type DocumentEvent = {
  event: string;
  subscription_id?: unknown;
  delivery?: unknown;
  as_of_seq?: unknown;
  order?: unknown;
  upsert?: unknown;
  code?: unknown;
  error?: unknown;
};

export type DocumentCommandSender = (payload: Record<string, unknown>) => void;

export function documentSubscribePayload(id: string, sub: DocumentSubscriber): Record<string, unknown> {
  const have = sub.have();
  return {
    cmd: 'doc_subscribe',
    subscription_id: id,
    query: {
      namespace: sub.request.namespace,
      collection: sub.request.collection,
      filters: (sub.request.filters ?? []).map((f) => ({
        field: f.field,
        op: f.op,
        // The one polymorphic leaf on the wire: a filter bound travels as JSON text so
        // the daemon can type-check it against the declared field.
        value_json: JSON.stringify(f.value ?? null),
      })),
      sort: sub.request.sort ? { field: sub.request.sort.field, desc: !!sub.request.sort.desc } : undefined,
      limit: sub.request.limit,
    },
    have: have.length > 0 ? have : undefined,
  };
}

// Ids are minted here and never reused, so a delivery from a subscription the
// client has already dropped names nothing and is discarded.
export class DocumentSubscriptions {
  private subs = new Map<string, DocumentSubscriber>();
  private nextId = 0;

  add(sub: DocumentSubscriber): string {
    this.nextId += 1;
    const id = `docsub-${this.nextId}`;
    this.subs.set(id, sub);
    return id;
  }

  remove(id: string): boolean {
    return this.subs.delete(id);
  }

  entries(): Array<[string, DocumentSubscriber]> {
    return Array.from(this.subs.entries());
  }

  get size(): number {
    return this.subs.size;
  }

  resubscribeAll(send: DocumentCommandSender): void {
    for (const [id, sub] of this.subs) {
      send(documentSubscribePayload(id, sub));
      sub.onLive(true);
    }
  }

  markDisconnected(): void {
    for (const sub of this.subs.values()) {
      sub.onLive(false);
    }
  }

  handleEvent(event: DocumentEvent): boolean {
    switch (event.event) {
      case 'doc_subscription_delivery': {
        const sub = this.subs.get(String(event.subscription_id ?? ''));
        if (!sub) return true;
        sub.onDelivery({
          delivery: typeof event.delivery === 'number' ? event.delivery : 0,
          asOfSeq: typeof event.as_of_seq === 'number' ? event.as_of_seq : 0,
          order: Array.isArray(event.order) ? (event.order as string[]) : [],
          upsert: Array.isArray(event.upsert) ? (event.upsert as StoredDocument[]) : [],
        });
        return true;
      }

      case 'doc_subscription_ended': {
        const id = String(event.subscription_id ?? '');
        const sub = this.subs.get(id);
        if (!sub) return true;
        // Forget it here before telling the subscriber, so a resubscribe it starts in
        // response mints a fresh id rather than colliding with this one.
        this.subs.delete(id);
        sub.onEnded(
          typeof event.code === 'string' ? event.code : '',
          typeof event.error === 'string' ? event.error : 'The subscription ended.',
        );
        return true;
      }

      default:
        return false;
    }
  }
}
