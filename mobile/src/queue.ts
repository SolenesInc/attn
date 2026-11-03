import type { GatewaySession } from '@attn/types/gatewayGenerated'
import { Schema } from 'effect'
import { PhoneAsk } from './asks/ask'
import { GardenSeed } from './garden/seed'
import { Pairing } from './pairing'

export const SessionState = Schema.Literals([
  'working',
  'waiting',
  'needs_approval',
  'idle',
  'scheduled',
  'launching',
  'recoverable',
  'unknown',
])
export type SessionState = typeof SessionState.Type

export const QueueSession = Schema.Struct({
  id: Schema.String,
  title: Schema.String,
  agent: Schema.String,
  state: SessionState,
  waitingSince: Schema.NullOr(Schema.Finite),
  host: Schema.NullOr(Schema.String),
  offline: Schema.Boolean,
  terminalId: Schema.NullOr(Schema.String),
})
export type QueueSession = typeof QueueSession.Type

export const LinkStatus = Schema.Literals(['connecting', 'online', 'home_offline', 'unreachable', 'closed'])
export type LinkStatus = typeof LinkStatus.Type

export const Home = Schema.Struct({
  pairing: Pairing,
  api: Schema.Finite,
  link: Schema.Finite,
  status: LinkStatus,
  reason: Schema.String,
  sessions: Schema.Array(QueueSession),
  listed: Schema.Boolean,
  asks: Schema.Array(PhoneAsk),
  garden: Schema.NullOr(Schema.Array(GardenSeed)),
})
export type Home = typeof Home.Type

export const fromGatewaySession = (session: GatewaySession): QueueSession => ({
  id: session.id,
  title: session.title,
  agent: session.agent,
  state: session.state,
  waitingSince: session.waiting_since ? session.waiting_since.getTime() : null,
  host: session.host ?? null,
  offline: session.offline,
  terminalId: session.terminal_id ?? null,
})

export type SessionRow = {
  readonly kind: 'session'
  readonly key: string
  readonly session: QueueSession
  readonly pairing: Pairing
  readonly live: boolean
}

export type HomeRow = {
  readonly kind: 'home'
  readonly key: string
  readonly pairing: Pairing
  readonly status: Exclude<LinkStatus, 'online'>
  readonly reason: string
}

export type QueueSections = {
  readonly waiting: ReadonlyArray<SessionRow>
  readonly working: ReadonlyArray<SessionRow>
  readonly rest: ReadonlyArray<SessionRow>
  readonly offline: ReadonlyArray<SessionRow | HomeRow>
}

const isWaiting = (state: SessionState) => state === 'waiting' || state === 'needs_approval'

const byTitle = (a: SessionRow, b: SessionRow) =>
  a.session.title.localeCompare(b.session.title) || a.key.localeCompare(b.key)

const longestWaitingFirst = (a: SessionRow, b: SessionRow) =>
  (a.session.waitingSince ?? Infinity) - (b.session.waitingSince ?? Infinity) || byTitle(a, b)

const homeServes = (home: Home) =>
  home.status === 'online' || (home.status === 'connecting' && home.sessions.length > 0)

export const buildQueue = (homes: ReadonlyArray<Home>): QueueSections => {
  const live: Array<SessionRow> = []
  const offline: Array<SessionRow | HomeRow> = []
  for (const home of homes) {
    const serving = homeServes(home)
    const rows = home.sessions.map((session): SessionRow => ({
      kind: 'session',
      key: `${home.pairing.id}/${session.id}`,
      session,
      pairing: home.pairing,
      live: serving && !session.offline,
    }))
    if (home.status !== 'online' && !serving) {
      offline.push({
        kind: 'home',
        key: home.pairing.id,
        pairing: home.pairing,
        status: home.status,
        reason: home.reason,
      })
    }
    live.push(...rows.filter((row) => row.live))
    offline.push(...rows.filter((row) => !row.live).toSorted(byTitle))
  }
  return {
    waiting: live.filter((row) => isWaiting(row.session.state)).toSorted(longestWaitingFirst),
    working: live.filter((row) => row.session.state === 'working').toSorted(byTitle),
    rest: live
      .filter((row) => !isWaiting(row.session.state) && row.session.state !== 'working')
      .toSorted(byTitle),
    offline,
  }
}
