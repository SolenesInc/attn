export function stewardManifest({ name = 'steward', reconcile = true } = {}) {
  return `name = "${name}"
description = "Derives one document per ticket. The reconcile exit proof's converging app."

attn_app_api = 1
entrypoint = "src/index.ts"
${reconcile ? 'reconcile = true\n' : ''}
[[subscribe]]
events = ["ticket.*"]

[[collections]]
name = "tickets"
fields = ["state"]

[[commands]]
name = "refresh"
description = "Ask the app to say what it holds."
`;
}

export const STEWARD_V1_DERIVE = `function derive(ticket: TicketLike): Record<string, unknown> {
  return { state: "seen", title: ticket.title }
}`;

export const STEWARD_V2_DERIVE = `function derive(ticket: TicketLike): Record<string, unknown> {
  // Version 2 derives a field version 1 never wrote. Every document already in
  // the collection is wrong until reconcile rebuilds it.
  return { state: "seen", title: ticket.title, status: ticket.status }
}`;

function blockGuard(releaseTicketId) {
  const id = JSON.stringify(releaseTicketId);
  return `  for (;;) {
    const gate = await ctx.current.snapshot()
    if (gate.tickets.some((row) => row.id === ${id})) break
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
`;
}

export function stewardEntrypoint(derive, { blockUntilTicket = null } = {}) {
  return `import type { Ctx, Handlers } from "./generated"
import type { AppEvent, ReconcileReason } from "@victorarias/attn-app"

interface TicketLike {
  readonly id: string
  readonly title: string
  readonly status: string
}

${derive}

async function onTicket(event: AppEvent, ctx: Ctx): Promise<void> {
  const current = await ctx.current.snapshot()
  const ticket = current.tickets.find((row) => row.id === event.subject)
  if (!ticket) {
    // The ticket is gone in current truth, so the fact was its removal.
    await ctx.collections.tickets.delete(event.subject)
    return
  }
  await ctx.collections.tickets.put(ticket.id, derive(ticket))
}

async function refresh(_payload: unknown, ctx: Ctx): Promise<{ held: number }> {
  return { held: (await ctx.collections.tickets.query({ limit: 1000 })).length }
}

async function reconcile(reason: ReconcileReason, ctx: Ctx): Promise<void> {
${blockUntilTicket ? blockGuard(blockUntilTicket) : ''}  const current = await ctx.current.snapshot()
  const live = new Map(current.tickets.map((row) => [row.id, row]))
  // Deleting what current truth no longer has is half the job. A rebuild that
  // only upserts leaves rows nothing will ever remove.
  for (const doc of await ctx.collections.tickets.query({ limit: 1000 })) {
    if (!live.has(doc.id)) {
      await ctx.collections.tickets.delete(doc.id)
    }
  }
  for (const ticket of live.values()) {
    await ctx.collections.tickets.put(ticket.id, derive(ticket))
  }
  console.log("steward: rebuilt " + live.size + " through seq " + reason.throughSeq)
}

export default {
  subscriptions: { "ticket.*": onTicket },
  commands: { refresh },
  reconcile,
} satisfies Handlers
`;
}

export function historianManifest({
  name = 'historian',
  description = 'Accumulates what it is told. Declares no reconcile on purpose.',
} = {}) {
  return `name = "${name}"
description = "${description}"

attn_app_api = 1
entrypoint = "src/index.ts"

[[subscribe]]
events = ["ticket.*"]

[[collections]]
name = "seen"
fields = ["state"]
`;
}

export function historianEntrypoint() {
  return `import type { Ctx, Handlers } from "./generated"
import type { AppEvent } from "@victorarias/attn-app"

// A history app: what it holds is what it was told, in the order it was told.
// No snapshot rebuilds this, which is exactly why attn refuses to move it across
// a trigger it cannot survive.
async function onTicket(event: AppEvent, ctx: Ctx): Promise<void> {
  await ctx.collections.seen.put(String(event.seq), {
    state: "seen",
    subject: event.subject,
    name: event.name,
  })
}

export default {
  subscriptions: { "ticket.*": onTicket },
} satisfies Handlers
`;
}
