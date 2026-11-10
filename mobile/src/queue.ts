import type {
  HomeGatewayCrewMemberBetweenSessions,
  HomeGatewaySession,
} from '@attn/types/homeGatewayGenerated'
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
  crewMember: Schema.NullOr(Schema.String),
  crewMemberName: Schema.NullOr(Schema.String),
  chief: Schema.Boolean,
})
export type QueueSession = typeof QueueSession.Type

export const CrewMemberBetweenSessions = Schema.Struct({
  crewMember: Schema.String,
  name: Schema.String,
  chief: Schema.Boolean,
})
export type CrewMemberBetweenSessions = typeof CrewMemberBetweenSessions.Type

export const LinkStatus = Schema.Literals(['connecting', 'online', 'home_offline', 'unreachable', 'closed'])
export type LinkStatus = typeof LinkStatus.Type

export const Home = Schema.Struct({
  pairing: Pairing,
  api: Schema.Finite,
  link: Schema.Finite,
  status: LinkStatus,
  reason: Schema.String,
  sessions: Schema.Array(QueueSession),
  crewBetweenSessions: Schema.Array(CrewMemberBetweenSessions),
  listed: Schema.Boolean,
  asks: Schema.Array(PhoneAsk),
  garden: Schema.NullOr(Schema.Array(GardenSeed)),
})
export type Home = typeof Home.Type

export const fromHomeGatewaySession = (session: HomeGatewaySession): QueueSession => ({
  id: session.id,
  title: session.title,
  agent: session.agent,
  state: session.state,
  waitingSince: session.waiting_since ? session.waiting_since.getTime() : null,
  host: session.host ?? null,
  offline: session.offline,
  terminalId: session.terminal_id ?? null,
  crewMember: session.crew_member ?? null,
  crewMemberName: session.crew_member_name ?? null,
  chief: session.chief ?? false,
})

export const fromHomeGatewayCrewMemberBetweenSessions = (
  member: HomeGatewayCrewMemberBetweenSessions,
): CrewMemberBetweenSessions => ({
  crewMember: member.crew_member,
  name: member.crew_member_name,
  chief: member.chief ?? false,
})

export const crewRowKey = (pairingId: string, crewMember: string) => `${pairingId}/crew/${crewMember}`

export type SessionRow = {
  readonly kind: 'session'
  readonly key: string
  readonly session: QueueSession
  readonly pairing: Pairing
  readonly live: boolean
}

export type BetweenSessionsRow = {
  readonly kind: 'betweenSessions'
  readonly key: string
  readonly member: CrewMemberBetweenSessions
  readonly pairing: Pairing
  readonly live: boolean
}

export type CrewRow = SessionRow | BetweenSessionsRow

export type HomeRow = {
  readonly kind: 'home'
  readonly key: string
  readonly pairing: Pairing
  readonly status: Exclude<LinkStatus, 'online'>
  readonly reason: string
}

export type QueueSections = {
  readonly crew: ReadonlyArray<CrewRow>
  readonly waiting: ReadonlyArray<SessionRow>
  readonly working: ReadonlyArray<SessionRow>
  readonly rest: ReadonlyArray<SessionRow>
  readonly offline: ReadonlyArray<SessionRow | HomeRow>
}

const isCrew = (session: QueueSession) => session.crewMember !== null || session.chief

export const isWaiting = (state: SessionState) => state === 'waiting' || state === 'needs_approval'

const byTitle = (a: SessionRow, b: SessionRow) =>
  a.session.title.localeCompare(b.session.title) || a.key.localeCompare(b.key)

const longestWaitingFirst = (a: SessionRow, b: SessionRow) =>
  (a.session.waitingSince ?? Infinity) - (b.session.waitingSince ?? Infinity) || byTitle(a, b)

const crewOrderKey = (row: CrewRow) =>
  row.kind === 'betweenSessions' ? row.member.crewMember : (row.session.crewMember ?? row.session.id)

const crewChief = (row: CrewRow) => (row.kind === 'betweenSessions' ? row.member.chief : row.session.chief)

const byText = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)

const chiefThenMember = (a: CrewRow, b: CrewRow) =>
  Number(crewChief(b)) - Number(crewChief(a)) ||
  byText(crewOrderKey(a), crewOrderKey(b)) ||
  byText(a.key, b.key)

const homeServes = (home: Home) =>
  home.status === 'online' || (home.status === 'connecting' && home.sessions.length > 0)

export const buildQueue = (homes: ReadonlyArray<Home>): QueueSections => {
  const crew: Array<CrewRow> = []
  const live: Array<SessionRow> = []
  const offline: Array<SessionRow | HomeRow> = []
  for (const home of homes) {
    const serving = homeServes(home)
    const rows = home.sessions.map((session): SessionRow => ({
      kind: 'session',
      key:
        session.crewMember === null
          ? `${home.pairing.id}/${session.id}`
          : crewRowKey(home.pairing.id, session.crewMember),
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
    crew.push(
      ...rows.filter((row) => isCrew(row.session)),
      ...home.crewBetweenSessions.map((member): BetweenSessionsRow => ({
        kind: 'betweenSessions',
        key: crewRowKey(home.pairing.id, member.crewMember),
        member,
        pairing: home.pairing,
        live: serving,
      })),
    )
    const others = rows.filter((row) => !isCrew(row.session))
    live.push(...others.filter((row) => row.live))
    offline.push(...others.filter((row) => !row.live).toSorted(byTitle))
  }
  return {
    crew: crew.toSorted(chiefThenMember),
    waiting: live.filter((row) => isWaiting(row.session.state)).toSorted(longestWaitingFirst),
    working: live.filter((row) => row.session.state === 'working').toSorted(byTitle),
    rest: live
      .filter((row) => !isWaiting(row.session.state) && row.session.state !== 'working')
      .toSorted(byTitle),
    offline,
  }
}

export const waitingAgents = (queue: QueueSections): number =>
  queue.waiting.length +
  queue.crew.filter((row) => row.kind === 'session' && row.live && isWaiting(row.session.state)).length
