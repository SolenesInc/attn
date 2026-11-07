import type { HomeGatewaySeed, HomeGatewaySeedDetail } from '@attn/types/homeGatewayGenerated'
import { Schema } from 'effect'

export const SeedStatus = Schema.Literals([
  'planted',
  'growing',
  'parked',
  'harvested',
  'withered',
  'unknown',
])
export type SeedStatus = typeof SeedStatus.Type

export const PlotProgress = Schema.Struct({
  total: Schema.Finite,
  done: Schema.Finite,
  withered: Schema.Finite,
  growing: Schema.Finite,
  parked: Schema.Finite,
  ready: Schema.Finite,
  blocked: Schema.Finite,
})
export type PlotProgress = typeof PlotProgress.Type

export const GardenSeed = Schema.Struct({
  id: Schema.String,
  slug: Schema.String,
  title: Schema.String,
  status: SeedStatus,
  tender: Schema.NullOr(Schema.String),
  claimed: Schema.Boolean,
  ready: Schema.Boolean,
  plotId: Schema.NullOr(Schema.String),
  waitsFor: Schema.Array(Schema.String),
  progress: Schema.NullOr(PlotProgress),
  checks: Schema.NullOr(Schema.Struct({ confirmed: Schema.Finite, total: Schema.Finite })),
  stateChangedAt: Schema.Finite,
  reason: Schema.NullOr(Schema.String),
})
export type GardenSeed = typeof GardenSeed.Type

export const SeedDetail = Schema.Struct({
  seed: GardenSeed,
  body: Schema.String,
  bodyTooLarge: Schema.NullOr(Schema.Struct({ bytes: Schema.Finite, limit: Schema.Finite })),
  checks: Schema.Array(
    Schema.Struct({
      id: Schema.String,
      text: Schema.String,
      state: Schema.Literals(['open', 'reported', 'confirmed', 'rejected', 'unknown']),
      from: Schema.NullOr(Schema.String),
    }),
  ),
  children: Schema.Array(GardenSeed),
  log: Schema.Array(
    Schema.Struct({
      id: Schema.String,
      kind: Schema.String,
      body: Schema.String,
      author: Schema.String,
      createdAt: Schema.Finite,
    }),
  ),
  logTotal: Schema.Finite,
})
export type SeedDetail = typeof SeedDetail.Type

export const fromHomeGatewaySeed = (seed: HomeGatewaySeed): GardenSeed => ({
  id: seed.id,
  slug: seed.slug,
  title: seed.title,
  status: seed.status,
  tender: seed.tender ?? null,
  claimed: seed.claimed,
  ready: seed.ready,
  plotId: seed.plot_id ?? null,
  waitsFor: seed.waits_for,
  progress: seed.progress ?? null,
  checks: seed.checks ?? null,
  stateChangedAt: seed.state_changed_at.getTime(),
  reason: seed.reason ?? null,
})

export const fromHomeGatewayDetail = (detail: HomeGatewaySeedDetail): SeedDetail => ({
  seed: fromHomeGatewaySeed(detail.seed),
  body: detail.body,
  bodyTooLarge:
    detail.body_too_large_bytes !== undefined
      ? { bytes: detail.body_too_large_bytes, limit: detail.max_frame_bytes ?? 0 }
      : null,
  checks: detail.checks.map((check) => ({
    id: check.id,
    text: check.text,
    state: check.state,
    from: check.from ?? null,
  })),
  children: detail.children.map(fromHomeGatewaySeed),
  log: detail.log.map((entry) => ({
    id: entry.id,
    kind: entry.kind,
    body: entry.body,
    author: entry.author,
    createdAt: entry.created_at.getTime(),
  })),
  logTotal: detail.log_total,
})

export const Verb = Schema.Literals(['park', 'harvest', 'wither', 'replant'])
export type Verb = typeof Verb.Type

export const verbSpecs: Record<Verb, { readonly label: string; readonly prompt: string }> = {
  park: { label: 'Park', prompt: 'What are you leaving it at?' },
  harvest: { label: 'Harvest', prompt: 'What got done?' },
  wither: { label: 'Wither', prompt: 'Why should nobody pick this up?' },
  replant: { label: 'Replant', prompt: 'Why is it open again?' },
}

export type GardenSections = {
  readonly plots: ReadonlyArray<GardenSeed>
  readonly growing: ReadonlyArray<GardenSeed>
  readonly ready: ReadonlyArray<GardenSeed>
  readonly blocked: ReadonlyArray<GardenSeed>
  readonly parked: ReadonlyArray<GardenSeed>
  readonly closed: ReadonlyArray<GardenSeed>
}

const isClosed = (seed: GardenSeed) => seed.status === 'harvested' || seed.status === 'withered'

export const gardenSections = (seeds: ReadonlyArray<GardenSeed>): GardenSections => {
  const openIds = new Set(seeds.filter((seed) => !isClosed(seed)).map((seed) => seed.id))
  const leaves = seeds.filter((seed) => seed.progress === null)
  const recent = (a: GardenSeed, b: GardenSeed) => b.stateChangedAt - a.stateChangedAt
  return {
    plots: seeds.filter(
      (seed) => seed.progress !== null && !isClosed(seed) && !(seed.plotId && openIds.has(seed.plotId)),
    ),
    growing: leaves.filter((seed) => seed.status === 'growing').toSorted(recent),
    ready: leaves.filter((seed) => seed.status === 'planted' && seed.ready),
    blocked: leaves.filter((seed) => seed.status === 'planted' && !seed.ready),
    parked: leaves.filter((seed) => seed.status === 'parked').toSorted(recent),
    closed: seeds.filter(isClosed).toSorted(recent),
  }
}
