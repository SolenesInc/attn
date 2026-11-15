import type { Html, HtmlBuilder } from 'foldkit/html'
import { verbsFor } from './verbs'
import { renderMarkdown, type MarkdownContext } from '../asks/markdown'
import type { Pairing } from '../pairing'
import type { LinkStatus } from '../queue'
import {
  gardenSections,
  verbSpecs,
  type GardenSeed,
  type PlotProgress,
  type SeedDetail,
  type Verb,
} from './seed'

export type GardenHome = {
  readonly pairing: Pairing
  readonly seeds: ReadonlyArray<GardenSeed> | null
  readonly status: LinkStatus
  readonly reason: string
  readonly tooOld: boolean
}

export type SeedPage =
  | { readonly status: 'loading' }
  | { readonly status: 'loaded'; readonly detail: SeedDetail }
  | { readonly status: 'failed'; readonly message: string }

export type NoteDraft = { readonly text: string; readonly sending: boolean; readonly error: string }

export type MoveDraft = {
  readonly verb: Verb
  readonly text: string
  readonly sending: boolean
  readonly error: string
}

export type OpenSeed = {
  readonly pairing: Pairing
  readonly live: boolean
  readonly page: SeedPage
  readonly note: NoteDraft
  readonly move: MoveDraft | null
  readonly now: number
}

export type GardenActions<Message> = MarkdownContext<Message> & {
  readonly openSeed: (pairingId: string, seedId: string) => Message
  readonly reload: Message
  readonly changeNote: (value: string) => Message
  readonly sendNote: Message
  readonly chooseMove: (verb: Verb) => Message
  readonly changeMoveText: (value: string) => Message
  readonly cancelMove: Message
  readonly confirmMove: Message
}

const statusLabel: Record<GardenSeed['status'], string> = {
  planted: 'planted',
  growing: 'growing',
  parked: 'parked',
  harvested: 'harvested',
  withered: 'withered',
  unknown: 'unknown',
}

export const progressText = (progress: PlotProgress): string =>
  [
    `${progress.done}/${progress.total} done`,
    progress.growing ? `${progress.growing} growing` : '',
    progress.ready ? `${progress.ready} ready` : '',
    progress.blocked ? `${progress.blocked} blocked` : '',
    progress.parked ? `${progress.parked} parked` : '',
    progress.withered ? `${progress.withered} withered` : '',
  ]
    .filter(Boolean)
    .join(' · ')

const seedLine = (seed: GardenSeed, plotTitle: string | null): string =>
  [
    seed.progress ? progressText(seed.progress) : '',
    seed.status === 'growing' && seed.tender ? seed.tender : '',
    seed.status === 'planted' && !seed.ready && seed.waitsFor.length > 0
      ? `waits for ${seed.waitsFor.length}`
      : '',
    seed.status === 'harvested' || seed.status === 'withered' ? (seed.reason ?? '') : '',
    plotTitle ?? '',
  ]
    .filter(Boolean)
    .join(' · ')

const checksBadge = <Message>(seed: GardenSeed, h: HtmlBuilder<Message>): Html =>
  seed.checks ? h.span([h.Class('age')], [`✓${seed.checks.confirmed}/${seed.checks.total}`]) : h.empty

const seedRow = <Message>(
  key: string,
  seed: GardenSeed,
  line: string,
  current: boolean,
  open: Message,
  h: HtmlBuilder<Message>,
): Html =>
  h.li(
    [h.Key(key), h.Class('ask-item')],
    [
      h.button(
        [
          h.Type('button'),
          h.Class(`row ask seed status-${seed.status}${current ? '' : ' offline'}`),
          h.DataAttribute('testid', 'seed-entry'),
          h.OnClick(open),
        ],
        [
          h.span(
            [h.Class('row-main')],
            [h.span([h.Class('row-title')], [seed.title]), h.span([h.Class('row-where')], [line])],
          ),
          h.span(
            [h.Class('row-side')],
            [
              h.span([h.Class('badge')], [statusLabel[seed.status]]),
              checksBadge(seed, h),
              h.span([h.Class('chevron'), h.AriaHidden(true)], ['›']),
            ],
          ),
        ],
      ),
    ],
  )

const section = <Message>(title: string, rows: ReadonlyArray<Html>, h: HtmlBuilder<Message>): Html =>
  rows.length === 0
    ? h.empty
    : h.section([h.Class('queue-section')], [h.h2([], [`${title} · ${rows.length}`]), h.ul([], [...rows])])

const where = (pairing: Pairing) => `${pairing.homeName} · ${pairing.profileName}`

const isCurrent = (home: GardenHome) => home.status === 'online' && home.seeds !== null

const staleLabel: Record<LinkStatus, string> = {
  online: 'loading',
  connecting: 'connecting',
  home_offline: 'offline',
  unreachable: 'unreachable',
  closed: 'not allowed',
}

const unavailableText = (home: GardenHome): string => {
  const name = where(home.pairing)
  const status = {
    online: `Loading the Garden from ${name}…`,
    connecting: `Connecting to ${name}…`,
    home_offline: `${name} is offline.`,
    unreachable: `Can’t reach ${name}.`,
    closed: [`${name} closed the connection.`, home.reason].filter(Boolean).join(' '),
  }[home.status]
  return home.seeds && home.seeds.length > 0 ? `${status} Its seeds are as of the last connection.` : status
}

const homeNotice = <Message>(home: GardenHome, h: HtmlBuilder<Message>): Html => {
  if (home.tooOld)
    return h.p(
      [h.Key(`old/${home.pairing.id}`), h.Class('muted small')],
      [`attn on ${where(home.pairing)} is too old to show the Garden. Update it to the latest attn.`],
    )
  if (isCurrent(home)) return h.empty
  return h.p(
    [h.Key(`away/${home.pairing.id}`), h.Class(`garden-home ${home.status}`)],
    [unavailableText(home)],
  )
}

export const gardenScreen = <Message>(
  homes: ReadonlyArray<GardenHome>,
  actions: GardenActions<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const many = homes.length > 1
  const rows = (pick: (sections: ReturnType<typeof gardenSections>) => ReadonlyArray<GardenSeed>) =>
    homes.flatMap((home) => {
      const seeds = home.seeds ?? []
      const current = isCurrent(home)
      const titles = new Map(seeds.map((seed) => [seed.id, seed.title]))
      return pick(gardenSections(seeds)).map((seed) =>
        seedRow(
          `${home.pairing.id}/${seed.id}`,
          seed,
          [
            seedLine(seed, seed.plotId ? (titles.get(seed.plotId) ?? null) : null),
            many || !current ? where(home.pairing) : '',
            current ? '' : staleLabel[home.status],
          ]
            .filter(Boolean)
            .join(' · '),
          current,
          actions.openSeed(home.pairing.id, seed.id),
          h,
        ),
      )
    })
  const shown = homes.some((home) => (home.seeds ?? []).length > 0)
  const known = homes.every((home) => home.seeds !== null || home.tooOld)
  return h.div(
    [h.Class('garden')],
    [
      ...homes.map((home) => homeNotice(home, h)),
      shown || !known ? h.empty : h.p([h.Class('empty muted')], ['Nothing is planted.']),
      section(
        'Plots',
        rows((s) => s.plots),
        h,
      ),
      section(
        'Growing',
        rows((s) => s.growing),
        h,
      ),
      section(
        'Ready',
        rows((s) => s.ready),
        h,
      ),
      section(
        'Blocked',
        rows((s) => s.blocked),
        h,
      ),
      section(
        'Parked',
        rows((s) => s.parked),
        h,
      ),
      section(
        'Closed today',
        rows((s) => s.closed),
        h,
      ),
    ],
  )
}

const checkMark: Record<SeedDetail['checks'][number]['state'], string> = {
  open: '○',
  reported: '◐',
  confirmed: '●',
  rejected: '✕',
  unknown: '?',
}

const moveForm = <Message>(
  move: MoveDraft,
  seed: GardenSeed,
  live: boolean,
  actions: GardenActions<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const spec = verbSpecs[move.verb]
  return h.div(
    [h.Class('move-form'), h.DataAttribute('testid', 'move-form')],
    [
      h.label([h.Class('move-prompt')], [spec.prompt]),
      h.textarea([
        h.AriaLabel(spec.prompt),
        h.Value(move.text),
        h.Rows(3),
        h.Disabled(move.sending),
        h.OnInput(actions.changeMoveText),
      ]),
      seed.claimed && seed.tender
        ? h.p(
            [h.Class('muted small')],
            [`${seed.tender} tends this seed; ${spec.label.toLowerCase()} moves it anyway.`],
          )
        : h.empty,
      move.error ? h.p([h.Class('error')], [move.error]) : h.empty,
      h.div(
        [h.Class('move-buttons')],
        [
          h.button(
            [h.Type('button'), h.Class('quiet'), h.Disabled(move.sending), h.OnClick(actions.cancelMove)],
            ['Cancel'],
          ),
          h.button(
            [
              h.Type('button'),
              h.Class('primary'),
              h.DataAttribute('testid', 'move-confirm'),
              h.Disabled(move.sending || !live || move.text.trim() === ''),
              h.OnClick(actions.confirmMove),
            ],
            [move.sending ? `${spec.label}ing…` : spec.label],
          ),
        ],
      ),
    ],
  )
}

export const seedScreen = <Message>(
  open: OpenSeed,
  age: (since: number, now: number) => string,
  actions: GardenActions<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const { page } = open
  if (page.status === 'loading') return h.p([h.Class('empty muted')], ['Loading…'])
  if (page.status === 'failed') {
    return h.div(
      [h.Class('empty')],
      [
        h.p([h.Class('error')], [page.message]),
        h.button([h.Class('primary'), h.OnClick(actions.reload)], ['Try again']),
      ],
    )
  }
  const { detail } = page
  const { seed } = detail
  const status = [
    statusLabel[seed.status],
    seed.claimed && seed.tender ? `tended by ${seed.tender}` : '',
    seed.status === 'planted' && seed.waitsFor.length > 0 ? `waits for ${seed.waitsFor.join(', ')}` : '',
    where(open.pairing),
  ]
    .filter(Boolean)
    .join(' · ')
  return h.article(
    [h.Class('ask-screen seed-screen'), h.DataAttribute('testid', 'seed-screen')],
    [
      h.h2([h.Class('subject')], [seed.title]),
      h.p([h.Class('ask-from')], [`${seed.slug} · ${seed.id}`]),
      h.p([h.Class(`seed-status status-${seed.status}`)], [status]),
      seed.reason ? h.p([h.Class('replied')], [seed.reason]) : h.empty,
      open.live
        ? h.empty
        : h.p(
            [h.Class('muted')],
            [`${open.pairing.homeName} is not connected; this is the last copy the phone read.`],
          ),
      open.move
        ? moveForm(open.move, seed, open.live, actions, h)
        : h.div(
            [h.Class('move-buttons')],
            verbsFor(seed).map((verb) =>
              h.button(
                [
                  h.Key(verb),
                  h.Type('button'),
                  h.Class('move'),
                  h.Disabled(!open.live),
                  h.OnClick(actions.chooseMove(verb)),
                ],
                [verbSpecs[verb].label],
              ),
            ),
          ),
      seed.progress ? h.p([h.Class('muted')], [progressText(seed.progress)]) : h.empty,
      detail.children.length > 0
        ? h.section(
            [h.Class('queue-section')],
            [
              h.h2([], [`Seeds · ${detail.children.length}`]),
              h.ul(
                [],
                detail.children.map((child) =>
                  seedRow(
                    child.id,
                    child,
                    seedLine(child, null),
                    open.live,
                    actions.openSeed(open.pairing.id, child.id),
                    h,
                  ),
                ),
              ),
            ],
          )
        : h.empty,
      detail.bodyTooLarge
        ? h.p(
            [h.Class('error')],
            [
              `This seed's body holds ${Math.ceil(detail.bodyTooLarge.bytes / 1024)} KiB, more than the ${Math.floor(detail.bodyTooLarge.limit / 1024)} KiB one phone message carries. Read it on your computer.`,
            ],
          )
        : h.div([h.Class('seed-body')], [renderMarkdown(detail.body, actions, h)]),
      detail.checks.length > 0
        ? h.section(
            [h.Class('queue-section')],
            [
              h.h2([], [`Checks · ${detail.checks.length}`]),
              h.ul(
                [h.Class('checks')],
                detail.checks.map((check) =>
                  h.li(
                    [h.Key(check.id), h.Class(`check row check-${check.state}`)],
                    [
                      h.span([h.Class('check-mark'), h.AriaLabel(check.state)], [checkMark[check.state]]),
                      h.span(
                        [h.Class('row-main')],
                        [h.span([h.Class('check-id')], [check.id]), ` ${check.text}`],
                      ),
                    ],
                  ),
                ),
              ),
            ],
          )
        : h.empty,
      h.section(
        [h.Class('queue-section')],
        [
          h.h2(
            [],
            [
              detail.log.length < detail.logTotal
                ? `Log · newest ${detail.log.length} of ${detail.logTotal}`
                : `Log · ${detail.logTotal}`,
            ],
          ),
          h.div(
            [h.Class('note-bar')],
            [
              h.textarea([
                h.Class('note'),
                h.AriaLabel('Note'),
                h.Placeholder('Add a note…'),
                h.Rows(2),
                h.Value(open.note.text),
                h.Disabled(open.note.sending),
                h.OnInput(actions.changeNote),
              ]),
              open.note.error ? h.p([h.Class('error')], [open.note.error]) : h.empty,
              h.button(
                [
                  h.Type('button'),
                  h.Class('primary'),
                  h.DataAttribute('testid', 'note-send'),
                  h.Disabled(open.note.sending || !open.live || open.note.text.trim() === ''),
                  h.OnClick(actions.sendNote),
                ],
                [open.note.sending ? 'Adding…' : 'Add note'],
              ),
            ],
          ),
          h.ul(
            [h.Class('log')],
            detail.log.map((entry) =>
              h.li(
                [
                  h.Key(entry.id),
                  h.Class(`log-entry kind-${entry.kind}`),
                  h.DataAttribute('testid', 'log-entry'),
                ],
                [
                  h.p(
                    [h.Class('row-where')],
                    [[entry.kind, entry.author, age(entry.createdAt, open.now)].join(' · ')],
                  ),
                  renderMarkdown(entry.body, actions, h),
                ],
              ),
            ),
          ),
        ],
      ),
    ],
  )
}
