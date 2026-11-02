import { App } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'
import { Duration, Effect, Queue, Schema, Stream } from 'effect'
import { Command, Runtime, Subscription, type Update } from 'foldkit'
import { type Document, type Html, type HtmlBuilder } from 'foldkit/html'
import { defineMessageUnion } from 'foldkit/message'
import { modifyFields } from 'foldkit/struct'
import { fromBase64Url } from './bytes'
import { deviceKeys } from './device'
import { pairWithHome, runLink, type LinkEvent } from './link'
import { Pairing, loadPairings, pairingFromOffer, readOffer, savePairings } from './pairing'
import { Home, QueueSession, buildQueue, fromGatewaySession, type HomeRow, type SessionRow } from './queue'

// MODEL

const PairingFlow = Schema.Literals(['idle', 'scanning', 'pairing', 'failed'])

export const Model = Schema.Struct({
  screen: Schema.Literals(['queue', 'pairings']),
  loaded: Schema.Boolean,
  foreground: Schema.Boolean,
  homes: Schema.Array(Home),
  pairingFlow: PairingFlow,
  pairingMessage: Schema.String,
  typedCode: Schema.String,
  now: Schema.Finite,
})
export type Model = typeof Model.Type

// MESSAGE

export const Message = defineMessageUnion({
  LoadedPairings: { pairings: Schema.Array(Pairing) },
  FailedLoadingPairings: { message: Schema.String },
  ChangedForeground: { foreground: Schema.Boolean },
  WelcomedByHome: { pairingId: Schema.String },
  ReceivedSessions: { pairingId: Schema.String, sessions: Schema.Array(QueueSession) },
  WentOffline: { pairingId: Schema.String },
  LostHome: { pairingId: Schema.String, reason: Schema.String },
  ClickedScan: {},
  ScannedCode: { code: Schema.String },
  CancelledScan: {},
  ChangedTypedCode: { value: Schema.String },
  SubmittedTypedCode: {},
  Paired: { pairing: Pairing },
  FailedPairing: { message: Schema.String },
  ClickedQueueTab: {},
  ClickedPairingsTab: {},
  ClickedRemovePairing: { pairingId: Schema.String },
  SavedPairings: {},
  Ticked: { now: Schema.Finite },
})
export type Message = typeof Message.Type

// COMMANDS

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error))

const LoadPairings = Command.define('LoadPairings', {
  messages: [Message.LoadedPairings, Message.FailedLoadingPairings],
  execute: Effect.tryPromise(() => loadPairings()).pipe(
    Effect.map((pairings) => Message.LoadedPairings({ pairings })),
    Effect.catch((error) => Effect.succeed(Message.FailedLoadingPairings({ message: errorText(error) }))),
  ),
})

const ScanCode = Command.define('ScanCode', {
  messages: [Message.ScannedCode, Message.CancelledScan, Message.FailedPairing],
  execute: Effect.tryPromise(async () => {
    const { CapacitorBarcodeScanner, CapacitorBarcodeScannerTypeHint } =
      await import('@capacitor/barcode-scanner')
    return CapacitorBarcodeScanner.scanBarcode({
      hint: CapacitorBarcodeScannerTypeHint.QR_CODE,
      scanInstructions: 'Scan the code in attn → Settings → Phones',
    })
  }).pipe(
    Effect.map(({ ScanResult }) =>
      ScanResult ? Message.ScannedCode({ code: ScanResult }) : Message.CancelledScan(),
    ),
    Effect.catch(({ cause }) => {
      const message = errorText(cause)
      return Effect.succeed(
        /cancel/i.test(message)
          ? Message.CancelledScan()
          : Message.FailedPairing({
              message: `The camera could not scan: ${message}. If camera access is off, turn it on in Settings → attn.`,
            }),
      )
    }),
  ),
})

const PairWithCode = Command.define('PairWithCode', {
  args: { code: Schema.String },
  messages: [Message.Paired, Message.FailedPairing],
  execute: ({ code }) =>
    Effect.tryPromise(async () => {
      const offer = readOffer(code)
      const keys = await deviceKeys()
      const welcome = await pairWithHome(
        {
          cloud: offer.cloud,
          homeId: offer.home_id,
          homeNoisePub: offer.home_noise_pub,
          homeName: offer.home_name,
          profileId: offer.profile_id,
        },
        keys,
        { pairingId: offer.pairing_id, secret: fromBase64Url(offer.secret) },
      )
      return pairingFromOffer(offer, offer.home_name || welcome.homeName, welcome.profileName)
    }).pipe(
      Effect.map((pairing) => Message.Paired({ pairing })),
      Effect.catch((error) =>
        Effect.succeed(Message.FailedPairing({ message: errorText(error.cause ?? error) })),
      ),
    ),
})

export const SavePairings = Command.define('SavePairings', {
  args: { pairings: Schema.Array(Pairing) },
  messages: [Message.SavedPairings],
  execute: ({ pairings }) =>
    Effect.promise(() => savePairings(pairings)).pipe(Effect.map(() => Message.SavedPairings())),
})

// UPDATE

const updateHome = (model: Model, pairingId: string, change: (home: Home) => Home): Model =>
  modifyFields(model, {
    homes: (homes) => homes.map((home) => (home.pairing.id === pairingId ? change(home) : home)),
  })

const newHome = (pairing: Pairing): Home => ({ pairing, status: 'connecting', reason: '', sessions: [] })

const saved = (homes: ReadonlyArray<Home>) => SavePairings({ pairings: homes.map((home) => home.pairing) })

export const update = (model: Model, message: Message) =>
  Message.match<Update.Return<Model, Message>>(message, {
    LoadedPairings: ({ pairings }) => ({
      model: modifyFields(model, { loaded: () => true, homes: () => pairings.map(newHome) }),
    }),
    FailedLoadingPairings: ({ message }) => ({
      model: modifyFields(model, {
        loaded: () => true,
        pairingFlow: () => 'failed' as const,
        pairingMessage: () => `Could not read this phone’s pairings: ${message}`,
      }),
    }),
    ChangedForeground: ({ foreground }) => ({
      model: modifyFields(model, {
        foreground: () => foreground,
        now: () => Date.now(),
        homes: (homes) =>
          foreground ? homes.map((home) => ({ ...home, status: 'connecting' as const, reason: '' })) : homes,
      }),
    }),
    WelcomedByHome: ({ pairingId }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, status: 'online', reason: '' })),
    }),
    ReceivedSessions: ({ pairingId, sessions }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, status: 'online', reason: '', sessions })),
    }),
    WentOffline: ({ pairingId }) => ({
      model: updateHome(model, pairingId, (home) => ({
        ...home,
        status: 'home_offline',
        reason: `${home.pairing.homeName} is offline`,
      })),
    }),
    LostHome: ({ pairingId, reason }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, status: 'unreachable', reason })),
    }),
    ClickedScan: () =>
      Capacitor.isNativePlatform()
        ? {
            model: modifyFields(model, { pairingFlow: () => 'scanning' as const, pairingMessage: () => '' }),
            commands: [ScanCode()],
          }
        : { model: modifyFields(model, { pairingFlow: () => 'idle' as const, pairingMessage: () => '' }) },
    ScannedCode: ({ code }) => ({
      model: modifyFields(model, { pairingFlow: () => 'pairing' as const }),
      commands: [PairWithCode({ code })],
    }),
    CancelledScan: () => ({ model: modifyFields(model, { pairingFlow: () => 'idle' as const }) }),
    ChangedTypedCode: ({ value }) => ({ model: modifyFields(model, { typedCode: () => value }) }),
    SubmittedTypedCode: () => ({
      model: modifyFields(model, { pairingFlow: () => 'pairing' as const, pairingMessage: () => '' }),
      commands: [PairWithCode({ code: model.typedCode.trim() })],
    }),
    Paired: ({ pairing }) => {
      const homes = [...model.homes.filter((home) => home.pairing.id !== pairing.id), newHome(pairing)]
      return {
        model: modifyFields(model, {
          homes: () => homes,
          pairingFlow: () => 'idle' as const,
          pairingMessage: () => `Paired with ${pairing.homeName} · ${pairing.profileName}`,
          typedCode: () => '',
          screen: () => 'queue' as const,
        }),
        commands: [saved(homes)],
      }
    },
    FailedPairing: ({ message }) => ({
      model: modifyFields(model, { pairingFlow: () => 'failed' as const, pairingMessage: () => message }),
    }),
    ClickedQueueTab: () => ({ model: modifyFields(model, { screen: () => 'queue' as const }) }),
    ClickedPairingsTab: () => ({ model: modifyFields(model, { screen: () => 'pairings' as const }) }),
    ClickedRemovePairing: ({ pairingId }) => {
      const homes = model.homes.filter((home) => home.pairing.id !== pairingId)
      return { model: modifyFields(model, { homes: () => homes }), commands: [saved(homes)] }
    },
    SavedPairings: () => ({ model }),
    Ticked: ({ now }) => ({ model: modifyFields(model, { now: () => now }) }),
  })

// INIT

export const init: Runtime.ApplicationInit<Model, Message> = () => ({
  model: {
    screen: 'queue',
    loaded: false,
    foreground: document.visibilityState === 'visible',
    homes: [],
    pairingFlow: 'idle',
    pairingMessage: '',
    typedCode: '',
    now: Date.now(),
  },
  commands: [LoadPairings()],
})

// SUBSCRIPTIONS

const linkMessage = (pairingId: string, event: LinkEvent): Message => {
  switch (event._tag) {
    case 'Welcomed':
      return Message.WelcomedByHome({ pairingId })
    case 'Sessions':
      return Message.ReceivedSessions({ pairingId, sessions: event.sessions.map(fromGatewaySession) })
    case 'HomeOffline':
      return Message.WentOffline({ pairingId })
    case 'Lost':
      return Message.LostHome({ pairingId, reason: event.reason })
  }
}

const linkStream = (pairing: Pairing) =>
  Stream.callback<Message>((queue) =>
    Effect.acquireRelease(
      Effect.promise(async () => {
        const keys = await deviceKeys()
        return runLink(pairing, keys, (event) => Queue.offerUnsafe(queue, linkMessage(pairing.id, event)))
      }),
      (close) => Effect.sync(close),
    ).pipe(Effect.flatMap(() => Effect.never)),
  )

const foregroundStream = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.promise(() =>
      App.addListener('appStateChange', ({ isActive }) =>
        Queue.offerUnsafe(queue, Message.ChangedForeground({ foreground: isActive })),
      ),
    ),
    (handle) => Effect.promise(() => handle.remove()),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const tickStream = Stream.tick(Duration.seconds(30)).pipe(
  Stream.map(() => Message.Ticked({ now: Date.now() })),
)

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  links: entry(
    { connected: Schema.Array(Pairing) },
    {
      modelToDependencies: (model) => ({
        connected: model.foreground ? model.homes.map((home) => home.pairing) : [],
      }),
      dependenciesToStream: ({ connected }) =>
        Stream.mergeAll(connected.map(linkStream), { concurrency: 'unbounded' }),
    },
  ),
  foreground: entry(
    { on: Schema.Boolean },
    { modelToDependencies: () => ({ on: true }), dependenciesToStream: () => foregroundStream },
  ),
  clock: entry(
    { ticking: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        ticking:
          model.foreground && model.homes.some((home) => home.sessions.some((s) => s.waitingSince !== null)),
      }),
      dependenciesToStream: ({ ticking }) => (ticking ? tickStream : Stream.empty),
    },
  ),
}))

// VIEW

const stateLabel: Record<QueueSession['state'], string> = {
  waiting: 'waiting',
  needs_approval: 'needs approval',
  working: 'working',
  idle: 'idle',
  scheduled: 'scheduled',
  launching: 'launching',
  recoverable: 'recoverable',
  unknown: 'unknown',
}

export const waitedFor = (since: number, now: number): string => {
  const minutes = Math.max(0, Math.floor((now - since) / 60_000))
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  return hours < 24 ? `${hours}h` : `${Math.floor(hours / 24)}d`
}

const where = (pairing: Pairing, host: string | null) =>
  [pairing.homeName, pairing.profileName, host].filter(Boolean).join(' · ')

const sessionRow = (row: SessionRow, now: number, h: HtmlBuilder<Message>): Html =>
  h.li(
    [
      h.Key(row.key),
      h.Class(`row session state-${row.session.state}${row.live ? '' : ' offline'}`),
      h.DataAttribute('testid', 'queue-entry'),
    ],
    [
      h.div(
        [h.Class('row-main')],
        [
          h.div([h.Class('row-title')], [row.session.title || row.session.id]),
          h.div([h.Class('row-where')], [`${row.session.agent} · ${where(row.pairing, row.session.host)}`]),
        ],
      ),
      h.div(
        [h.Class('row-side')],
        [
          h.span([h.Class('badge')], [row.live ? stateLabel[row.session.state] : 'offline']),
          row.live && row.session.waitingSince !== null
            ? h.span([h.Class('age')], [waitedFor(row.session.waitingSince, now)])
            : h.empty,
        ],
      ),
    ],
  )

const homeStatusLabel: Record<HomeRow['status'], string> = {
  connecting: 'connecting…',
  home_offline: 'offline',
  unreachable: 'unreachable',
}

const homeRow = (row: HomeRow, h: HtmlBuilder<Message>): Html =>
  h.li(
    [h.Key(row.key), h.Class(`row home ${row.status}`), h.DataAttribute('testid', 'queue-entry')],
    [
      h.div(
        [h.Class('row-main')],
        [
          h.div([h.Class('row-title')], [where(row.pairing, null)]),
          row.reason ? h.div([h.Class('row-where')], [row.reason]) : h.empty,
        ],
      ),
      h.div([h.Class('row-side')], [h.span([h.Class('badge')], [homeStatusLabel[row.status]])]),
    ],
  )

const section = (title: string, rows: ReadonlyArray<Html>, h: HtmlBuilder<Message>): Html =>
  rows.length === 0
    ? h.empty
    : h.section([h.Class('queue-section')], [h.h2([], [`${title} · ${rows.length}`]), h.ul([], [...rows])])

const pairHint = (h: HtmlBuilder<Message>): Html =>
  h.div(
    [h.Class('empty')],
    [
      h.p([], ['Pair this phone with attn to see your agents here.']),
      h.p([h.Class('muted')], ['On your computer, open attn → Settings → Phones and choose Pair.']),
      h.button([h.Class('primary'), h.OnClick(Message.ClickedPairingsTab())], ['Pair a home']),
    ],
  )

const queueView = (model: Model, h: HtmlBuilder<Message>): Html => {
  if (!model.loaded) return h.div([h.Class('empty')], [])
  if (model.homes.length === 0) return pairHint(h)
  const queue = buildQueue(model.homes)
  const sessions = (rows: ReadonlyArray<SessionRow>) => rows.map((row) => sessionRow(row, model.now, h))
  const nothing = queue.waiting.length + queue.working.length + queue.rest.length + queue.offline.length === 0
  return h.div(
    [h.Class('queue')],
    [
      nothing ? h.p([h.Class('empty muted')], ['No agents yet.']) : h.empty,
      section('Waiting', sessions(queue.waiting), h),
      section('Working', sessions(queue.working), h),
      section('Other agents', sessions(queue.rest), h),
      section(
        'Offline',
        queue.offline.map((row) => (row.kind === 'home' ? homeRow(row, h) : sessionRow(row, model.now, h))),
        h,
      ),
    ],
  )
}

const homeStatusText = (home: Home): string =>
  home.status === 'online' ? 'online' : home.status === 'connecting' ? 'connecting…' : home.reason

const pairingsView = (model: Model, h: HtmlBuilder<Message>): Html =>
  h.div(
    [h.Class('pairings')],
    [
      h.ul(
        [],
        model.homes.map((home) =>
          h.li(
            [
              h.Key(home.pairing.id),
              h.Class(`row pairing ${home.status}`),
              h.DataAttribute('testid', 'pairing-row'),
            ],
            [
              h.div(
                [h.Class('row-main')],
                [
                  h.div([h.Class('row-title')], [where(home.pairing, null)]),
                  h.div([h.Class('row-where')], [homeStatusText(home)]),
                ],
              ),
              h.button(
                [h.Class('quiet'), h.OnClick(Message.ClickedRemovePairing({ pairingId: home.pairing.id }))],
                ['Forget'],
              ),
            ],
          ),
        ),
      ),
      model.homes.length > 0
        ? h.p(
            [h.Class('muted small')],
            [
              'Forget removes a pairing from this phone. To stop this phone reaching a home, revoke it in attn → Settings → Phones.',
            ],
          )
        : h.empty,
      Capacitor.isNativePlatform()
        ? h.button(
            [
              h.Class('primary'),
              h.OnClick(Message.ClickedScan()),
              h.Disabled(model.pairingFlow === 'pairing'),
            ],
            [model.pairingFlow === 'pairing' ? 'Pairing…' : 'Scan a pairing code'],
          )
        : h.form(
            [h.Class('typed-code'), h.OnSubmit(Message.SubmittedTypedCode())],
            [
              h.label([], ['Pairing code (the QR code’s text)']),
              h.textarea([
                h.Value(model.typedCode),
                h.OnInput((value) => Message.ChangedTypedCode({ value })),
                h.Rows(3),
                h.Spellcheck(false),
              ]),
              h.button(
                [h.Type('submit'), h.Class('primary'), h.Disabled(model.pairingFlow === 'pairing')],
                [model.pairingFlow === 'pairing' ? 'Pairing…' : 'Pair'],
              ),
            ],
          ),
      model.pairingMessage
        ? h.p([h.Class(model.pairingFlow === 'failed' ? 'error' : 'muted')], [model.pairingMessage])
        : h.empty,
    ],
  )

const waitingCount = (model: Model) => buildQueue(model.homes).waiting.length

export const view = (model: Model, h: HtmlBuilder<Message>): Document => ({
  title: 'attn',
  body: h.div(
    [h.Class('app')],
    [
      h.header(
        [h.Class('top')],
        [
          h.h1([], [model.screen === 'queue' ? 'Queue' : 'Pairings']),
          model.screen === 'queue' && waitingCount(model) > 0
            ? h.span([h.Class('waiting-count')], [`${waitingCount(model)} waiting`])
            : h.empty,
        ],
      ),
      h.main([], [model.screen === 'queue' ? queueView(model, h) : pairingsView(model, h)]),
      h.nav(
        [h.Class('tabs')],
        [
          h.button(
            [h.Class(model.screen === 'queue' ? 'on' : ''), h.OnClick(Message.ClickedQueueTab())],
            ['Queue'],
          ),
          h.button(
            [h.Class(model.screen === 'pairings' ? 'on' : ''), h.OnClick(Message.ClickedPairingsTab())],
            ['Pairings'],
          ),
        ],
      ),
    ],
  ),
})
