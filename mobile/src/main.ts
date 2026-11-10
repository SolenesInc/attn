import { App } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'
import { Keyboard } from '@capacitor/keyboard'
import { Duration, Effect, Option, Queue, Schema, Stream } from 'effect'
import { renderMermaid } from '@attn/components/Markdown/mermaidRender'
import { Command, Mount, Runtime, Subscription, type Update } from 'foldkit'
import { type Document, type Html, type HtmlBuilder } from 'foldkit/html'
import { defineMessageUnion } from 'foldkit/message'
import { modifyFields } from 'foldkit/struct'
import {
  PhoneAsk,
  Draft,
  answersInOneTap,
  recommendedAnswers,
  askFrom,
  askKey,
  draftAnswers,
  emptyAnswer,
  emptyDraft,
  fromHomeGatewayAsk,
  type Answer,
} from './asks/ask'
import { ImageRefused, fetchAskImage } from './asks/images'
import { keepParsed, visualsOf, type DiagramView, type ImageView } from './asks/markdown'
import { askRow, askScreen, type AskActions, type AskRow } from './asks/view'
import { attachZoom } from './asks/zoom'
import { fromBase64Url } from './bytes'
import { GardenSeed, SeedDetail, Verb, fromHomeGatewayDetail, fromHomeGatewaySeed } from './garden/seed'
import { gardenScreen, seedScreen, type GardenActions } from './garden/view'
import { phoneKeys } from './phoneKeys'
import { homeGardenApi, homeTerminalApi, pairWithHome, runLink, type Link, type LinkEvent } from './link'
import {
  LinkTarget,
  Pairing,
  linkTarget,
  loadPairings,
  pairingFromCode,
  pairingId,
  readPairingCode,
  savePairings,
} from './pairing'
import { readPushTap, registerForPush, watchPushTokens } from './push'
import {
  Home,
  QueueSession,
  CrewMemberBetweenSessions,
  buildQueue,
  crewRowKey,
  fromHomeGatewayCrewMemberBetweenSessions,
  fromHomeGatewaySession,
  waitingAgents,
  type BetweenSessionsRow,
  type HomeRow,
  type SessionRow,
} from './queue'
import { activeTerminal, mountTerminal, terminalKeys, type TerminalKey } from './terminal/terminal'

// MODEL

const PairingFlow = Schema.Literals(['idle', 'scanning', 'pairing', 'failed'])

const ImageState = Schema.Union([
  Schema.Struct({ status: Schema.Literal('loading') }),
  Schema.Struct({ status: Schema.Literal('loaded'), url: Schema.String }),
  Schema.Struct({ status: Schema.Literal('failed'), message: Schema.String, retry: Schema.Boolean }),
])
type ImageState = typeof ImageState.Type

const DiagramState = Schema.Union([
  Schema.Struct({ status: Schema.Literal('loading') }),
  Schema.Struct({ status: Schema.Literal('rendered'), svg: Schema.String, width: Schema.Finite }),
  Schema.Struct({ status: Schema.Literal('failed'), message: Schema.String }),
])

const Zoom = Schema.Union([
  Schema.Struct({ kind: Schema.Literal('image'), url: Schema.String, alt: Schema.String }),
  Schema.Struct({ kind: Schema.Literal('diagram'), svg: Schema.String }),
])

const OpenAskId = Schema.Struct({ pairingId: Schema.String, askId: Schema.String })

const AnswerState = Schema.Union([
  Schema.Struct({ status: Schema.Literal('sending') }),
  Schema.Struct({ status: Schema.Literal('failed'), message: Schema.String }),
])

const OpenSession = Schema.Struct({ pairingId: Schema.String, sessionId: Schema.String })

const Composer = Schema.Struct({
  text: Schema.String,
  sending: Schema.Boolean,
  note: Schema.String,
})
export type Composer = typeof Composer.Type

const OpenSeedId = Schema.Struct({ pairingId: Schema.String, seedId: Schema.String })

const SeedPage = Schema.Union([
  Schema.Struct({ status: Schema.Literal('loading') }),
  Schema.Struct({ status: Schema.Literal('loaded'), detail: SeedDetail }),
  Schema.Struct({ status: Schema.Literal('failed'), message: Schema.String }),
])

const NoteDraft = Schema.Struct({ text: Schema.String, sending: Schema.Boolean, error: Schema.String })
type NoteDraft = typeof NoteDraft.Type

const MoveDraft = Schema.Struct({
  verb: Verb,
  text: Schema.String,
  sending: Schema.Boolean,
  error: Schema.String,
})

const PushToken = Schema.Struct({
  token: Schema.String,
  environment: Schema.Literals(['sandbox', 'production']),
})

export const CrewStart = Schema.Union([
  Schema.Struct({ status: Schema.Literal('starting') }),
  Schema.Struct({ status: Schema.Literal('refused'), message: Schema.String }),
])
export type CrewStart = typeof CrewStart.Type

export const Model = Schema.Struct({
  screen: Schema.Literals(['queue', 'pairings', 'ask', 'session', 'garden', 'seed']),
  seedTrail: Schema.Array(OpenSeedId),
  mentionVersion: Schema.optionalKey(Schema.Finite),
  mentions: Schema.optionalKey(Schema.Array(Schema.Struct({ text: Schema.String, seed_id: Schema.String }))),
  mentionError: Schema.optionalKey(Schema.String),
  seedPage: SeedPage,
  seedNotes: Schema.Record(Schema.String, NoteDraft),
  seedMove: Schema.NullOr(MoveDraft),
  openSession: Schema.NullOr(OpenSession),
  composers: Schema.Record(Schema.String, Composer),
  terminalStatus: Schema.String,
  fullHeight: Schema.Finite,
  keyboardHeight: Schema.Finite,
  openAsk: Schema.NullOr(OpenAskId),
  drafts: Schema.Record(Schema.String, Draft),
  answering: Schema.Record(Schema.String, AnswerState),
  crewStarts: Schema.Record(Schema.String, CrewStart),
  images: Schema.Record(Schema.String, ImageState),
  diagrams: Schema.Record(Schema.String, DiagramState),
  zoom: Schema.NullOr(Zoom),
  loaded: Schema.Boolean,
  foreground: Schema.Boolean,
  homes: Schema.Array(Home),
  pairingFlow: PairingFlow,
  pairingMessage: Schema.String,
  typedCode: Schema.String,
  pushToken: Schema.NullOr(PushToken),
  pushNote: Schema.String,
  pushRefusals: Schema.Record(Schema.String, Schema.String),
  now: Schema.Finite,
})
export type Model = typeof Model.Type

// MESSAGE

export const Message = defineMessageUnion({
  LoadedPairings: { pairings: Schema.Array(Pairing) },
  FailedLoadingPairings: { message: Schema.String },
  ChangedForeground: { foreground: Schema.Boolean },
  WelcomedByHome: {
    pairingId: Schema.String,
    api: Schema.Finite,
    homeName: Schema.String,
    profileName: Schema.String,
  },
  ReceivedSessions: {
    pairingId: Schema.String,
    sessions: Schema.Array(QueueSession),
    crewBetweenSessions: Schema.optionalKey(Schema.Array(CrewMemberBetweenSessions)),
  },
  ReceivedAsks: { pairingId: Schema.String, asks: Schema.Array(PhoneAsk) },
  ReceivedGarden: { pairingId: Schema.String, seeds: Schema.Array(GardenSeed) },
  ChangedSeed: { pairingId: Schema.String, seedId: Schema.String },
  ClickedGardenTab: {},
  ClickedSeed: { pairingId: Schema.String, seedId: Schema.String },
  LoadedMentions: {
    pairingId: Schema.String,
    askId: Schema.String,
    version: Schema.Finite,
    mentions: Schema.Array(Schema.Struct({ text: Schema.String, seed_id: Schema.String })),
  },
  FailedMentions: {
    pairingId: Schema.String,
    askId: Schema.String,
    version: Schema.Finite,
    message: Schema.String,
  },
  LoadedSeed: { pairingId: Schema.String, seedId: Schema.String, detail: SeedDetail },
  FailedSeed: { pairingId: Schema.String, seedId: Schema.String, message: Schema.String },
  ClickedReloadSeed: {},
  ChangedSeedNote: { value: Schema.String },
  ClickedAddNote: {},
  AddedNote: { key: Schema.String, text: Schema.String },
  FailedNote: { key: Schema.String, message: Schema.String },
  ClickedMove: { verb: Verb },
  ChangedMoveText: { value: Schema.String },
  CancelledMove: {},
  ClickedConfirmMove: {},
  MovedSeed: { key: Schema.String },
  FailedMove: { key: Schema.String, message: Schema.String },
  ClickedAsk: { pairingId: Schema.String, askId: Schema.String },
  ClickedBack: {},
  PickedChoice: { key: Schema.String, label: Schema.String },
  ChangedOther: { key: Schema.String, value: Schema.String },
  ChangedText: { key: Schema.String, value: Schema.String },
  ChangedNote: { value: Schema.String },
  ClickedAcceptRecommended: {},
  ClickedSend: {},
  SentAnswer: { pairingId: Schema.String, askId: Schema.String },
  FailedAnswer: { pairingId: Schema.String, askId: Schema.String, message: Schema.String },
  LoadedImage: { key: Schema.String, url: Schema.String },
  FailedImage: { key: Schema.String, message: Schema.String, retry: Schema.Boolean },
  RenderedDiagram: { code: Schema.String, svg: Schema.String, width: Schema.Finite },
  FailedDiagram: { code: Schema.String, message: Schema.String },
  ReleasedImages: {},
  ClickedZoomImage: { url: Schema.String, alt: Schema.String },
  ClickedZoomDiagram: { svg: Schema.String },
  ClosedZoom: {},
  AttachedZoom: {},
  WentOffline: { pairingId: Schema.String },
  LostHome: { pairingId: Schema.String, reason: Schema.String },
  ClosedByHome: { pairingId: Schema.String, reason: Schema.String },
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
  ClickedSession: { pairingId: Schema.String, sessionId: Schema.String },
  ChangedComposer: { value: Schema.String },
  SubmittedComposer: {},
  ClickedMemberBetweenSessions: { pairingId: Schema.String, crewMember: Schema.String },
  AcceptedStart: { key: Schema.String },
  RefusedStart: { key: Schema.String, message: Schema.String },
  DeliveredPrompt: { key: Schema.String, text: Schema.String },
  FailedPrompt: { key: Schema.String, message: Schema.String },
  StartedLink: { pairingId: Schema.String, link: Schema.Finite },
  ClickedKey: { key: Schema.Literals(terminalKeys) },
  ClickedHideKeyboard: {},
  TappedTerminal: {},
  ChangedKeyboard: { height: Schema.Finite },
  ChangedViewport: { height: Schema.Finite },
  ChangedTerminalStatus: { text: Schema.String },
  CompletedCommand: {},
  ReceivedPushToken: { pushToken: PushToken },
  RefusedPush: { message: Schema.String },
  RegisteredPush: { homeKey: Schema.String },
  FailedPush: { homeKey: Schema.String, message: Schema.String },
  TappedNotification: { pairingId: Schema.String, sessionId: Schema.String, askId: Schema.String },
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
      const pairingCode = readPairingCode(code)
      const keys = await phoneKeys()
      const welcome = await pairWithHome(
        {
          cloud: pairingCode.cloud,
          homeId: pairingCode.home_id,
          homeNoisePub: pairingCode.home_noise_pub,
          homeName: pairingCode.home_name,
          profileId: pairingCode.profile_id,
        },
        keys,
        { pairingId: pairingCode.pairing_id, secret: fromBase64Url(pairingCode.secret) },
      )
      return pairingFromCode(pairingCode, welcome.homeName, welcome.profileName, Date.now())
    }).pipe(
      Effect.map((pairing) => Message.Paired({ pairing })),
      Effect.catch((error) =>
        Effect.succeed(Message.FailedPairing({ message: errorText(error.cause ?? error) })),
      ),
    ),
})

export const pushHomeKey = (cloud: string, homeId: string) => `${cloud} ${homeId}`

export const RegisterPush = Command.define('RegisterPush', {
  args: { cloud: Schema.String, homeId: Schema.String, pushToken: PushToken },
  messages: [Message.RegisteredPush, Message.FailedPush],
  execute: ({ cloud, homeId, pushToken }) =>
    Effect.tryPromise(() => registerForPush({ cloud, homeId }, pushToken)).pipe(
      Effect.map(() => Message.RegisteredPush({ homeKey: pushHomeKey(cloud, homeId) })),
      Effect.catch((error) =>
        Effect.succeed(
          Message.FailedPush({
            homeKey: pushHomeKey(cloud, homeId),
            message: errorText(error.cause ?? error),
          }),
        ),
      ),
    ),
})

const registerWith = (homes: ReadonlyArray<Home>, pushToken: typeof PushToken.Type) => {
  const seen = new Set<string>()
  return homes.flatMap(({ pairing: { cloud, homeId } }) => {
    const key = pushHomeKey(cloud, homeId)
    if (seen.has(key)) return []
    seen.add(key)
    return [RegisterPush({ cloud, homeId, pushToken })]
  })
}

export const SavePairings = Command.define('SavePairings', {
  args: { pairings: Schema.Array(Pairing) },
  messages: [Message.SavedPairings],
  execute: ({ pairings }) =>
    Effect.promise(() => savePairings(pairings)).pipe(Effect.map(() => Message.SavedPairings())),
})

export const liveLinks = new Map<string, Link>()

const linkTo = (pairingId: string): Link => {
  const link = liveLinks.get(pairingId)
  if (!link) throw new Error('this home is not connected')
  return link
}

export const imageKey = (pairingId: string, askId: string, src: string) => `${pairingId}\n${askId}\n${src}`

export const LoadImage = Command.define('LoadImage', {
  args: { pairingId: Schema.String, askId: Schema.String, src: Schema.String },
  messages: [Message.LoadedImage, Message.FailedImage],
  execute: ({ pairingId, askId, src }) =>
    Effect.tryPromise(async () =>
      URL.createObjectURL(await fetchAskImage(linkTo(pairingId), askId, src)),
    ).pipe(
      Effect.map((url) => Message.LoadedImage({ key: imageKey(pairingId, askId, src), url })),
      Effect.catch(({ cause }) =>
        Effect.succeed(
          Message.FailedImage({
            key: imageKey(pairingId, askId, src),
            message:
              cause instanceof ImageRefused ? cause.message : `Could not load ${src}: ${errorText(cause)}`,
            retry: !(cause instanceof ImageRefused),
          }),
        ),
      ),
    ),
})

const ReleaseImages = Command.define('ReleaseImages', {
  args: { urls: Schema.Array(Schema.String) },
  messages: [Message.ReleasedImages],
  execute: ({ urls }) =>
    Effect.sync(() => {
      for (const url of urls) URL.revokeObjectURL(url)
      return Message.ReleasedImages()
    }),
})

let diagramCount = 0

const topToBottom = (code: string) => code.replace(/^(\s*(?:graph|flowchart)\s+)(?:LR|RL)\b/m, '$1TB')

const naturalWidth = (svg: string) => Number(/viewBox="[\d.-]+ [\d.-]+ ([\d.]+)/.exec(svg)?.[1] ?? 0)

export const RenderDiagram = Command.define('RenderDiagram', {
  args: { code: Schema.String },
  messages: [Message.RenderedDiagram, Message.FailedDiagram],
  execute: ({ code }) =>
    Effect.tryPromise(() =>
      renderMermaid(
        `ask-diagram-${diagramCount++}`,
        topToBottom(code),
        window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'neutral',
      ),
    ).pipe(
      Effect.map((svg) => Message.RenderedDiagram({ code, svg, width: naturalWidth(svg) })),
      Effect.catch(({ cause }) => Effect.succeed(Message.FailedDiagram({ code, message: errorText(cause) }))),
    ),
})

export const SendAnswer = Command.define('SendAnswer', {
  args: {
    pairingId: Schema.String,
    askId: Schema.String,
    answers: Schema.Array(Schema.Unknown),
    note: Schema.String,
  },
  messages: [Message.SentAnswer, Message.FailedAnswer],
  execute: ({ pairingId, askId, answers, note }) =>
    Effect.tryPromise(async () => {
      const reply = await linkTo(pairingId).request({ cmd: 'ask_answer', ask_id: askId, answers, note })
      if (reply.event !== 'ask_answer_result') throw new Error(`the home answered ${reply.event}`)
      if (reply.error) throw new Error(reply.error)
    }).pipe(
      Effect.map(() => Message.SentAnswer({ pairingId, askId })),
      Effect.catch(({ cause }) =>
        Effect.succeed(Message.FailedAnswer({ pairingId, askId, message: errorText(cause) })),
      ),
    ),
})

export const SendPrompt = Command.define('SendPrompt', {
  args: { key: Schema.String, pairingId: Schema.String, sessionId: Schema.String, text: Schema.String },
  messages: [Message.DeliveredPrompt, Message.FailedPrompt],
  execute: ({ key, pairingId, sessionId, text }) =>
    Effect.tryPromise(async () => {
      const reply = await linkTo(pairingId).request({ cmd: 'submit_prompt', session_id: sessionId, text })
      if (reply.event !== 'submit_result') throw new Error(`the home answered ${reply.event}`)
      if (!reply.delivered) throw new Error(reply.message || 'Not sent.')
    }).pipe(
      Effect.map(() => Message.DeliveredPrompt({ key, text })),
      Effect.catch(({ cause }) => Effect.succeed(Message.FailedPrompt({ key, message: errorText(cause) }))),
    ),
})

export const StartCrewMember = Command.define('StartCrewMember', {
  args: { key: Schema.String, pairingId: Schema.String, crewMember: Schema.String },
  messages: [Message.AcceptedStart, Message.RefusedStart],
  execute: ({ key, pairingId, crewMember }) =>
    Effect.tryPromise(async () => {
      const reply = await linkTo(pairingId).request({ cmd: 'crew_start', crew_member: crewMember })
      if (reply.event !== 'crew_start_result') throw new Error(`the home answered ${reply.event}`)
      if (reply.error) throw new Error(reply.error)
    }).pipe(
      Effect.map(() => Message.AcceptedStart({ key })),
      Effect.catch(({ cause }) => Effect.succeed(Message.RefusedStart({ key, message: errorText(cause) }))),
    ),
})

export const GetSeed = Command.define('GetSeed', {
  args: { pairingId: Schema.String, seedId: Schema.String },
  messages: [Message.LoadedSeed, Message.FailedSeed],
  execute: ({ pairingId, seedId }) =>
    Effect.tryPromise(async () => {
      const reply = await linkTo(pairingId).request({ cmd: 'seed_get', seed_id: seedId })
      if (reply.event === 'seed_error') throw new Error(reply.message)
      if (reply.event !== 'seed_detail') throw new Error(`the home answered ${reply.event}`)
      return fromHomeGatewayDetail(reply)
    }).pipe(
      Effect.map((detail) => Message.LoadedSeed({ pairingId, seedId, detail })),
      Effect.catch(({ cause }) =>
        Effect.succeed(Message.FailedSeed({ pairingId, seedId, message: errorText(cause) })),
      ),
    ),
})

export const GetAskSeedMentions = Command.define('GetAskSeedMentions', {
  args: { pairingId: Schema.String, askId: Schema.String, version: Schema.Finite },
  messages: [Message.LoadedMentions, Message.FailedMentions],
  execute: ({ pairingId, askId, version }) =>
    Effect.tryPromise(async () => {
      const reply = await linkTo(pairingId).request({ cmd: 'ask_seed_mentions', ask_id: askId })
      if (reply.event !== 'ask_seed_mentions_result') throw new Error(`the home answered ${reply.event}`)
      if (reply.error) throw new Error(reply.error)
      return reply.mentions
    }).pipe(
      Effect.map((mentions) => Message.LoadedMentions({ pairingId, askId, version, mentions })),
      Effect.catch(({ cause }) =>
        Effect.succeed(Message.FailedMentions({ pairingId, askId, version, message: errorText(cause) })),
      ),
    ),
})

const seedResult = async (
  pairingId: string,
  command: { readonly cmd: string; readonly [field: string]: unknown },
) => {
  const reply = await linkTo(pairingId).request(command)
  if (reply.event !== 'seed_result') throw new Error(`the home answered ${reply.event}`)
  if (reply.error) throw new Error(reply.error)
}

export const AddNote = Command.define('AddNote', {
  args: { key: Schema.String, pairingId: Schema.String, seedId: Schema.String, body: Schema.String },
  messages: [Message.AddedNote, Message.FailedNote],
  execute: ({ key, pairingId, seedId, body }) =>
    Effect.tryPromise(() => seedResult(pairingId, { cmd: 'seed_note', seed_id: seedId, body })).pipe(
      Effect.map(() => Message.AddedNote({ key, text: body })),
      Effect.catch(({ cause }) => Effect.succeed(Message.FailedNote({ key, message: errorText(cause) }))),
    ),
})

export const MoveSeed = Command.define('MoveSeed', {
  args: {
    key: Schema.String,
    pairingId: Schema.String,
    seedId: Schema.String,
    verb: Verb,
    text: Schema.String,
  },
  messages: [Message.MovedSeed, Message.FailedMove],
  execute: ({ key, pairingId, seedId, verb, text }) =>
    Effect.tryPromise(() => seedResult(pairingId, { cmd: 'seed_move', seed_id: seedId, verb, text })).pipe(
      Effect.map(() => Message.MovedSeed({ key })),
      Effect.catch(({ cause }) => Effect.succeed(Message.FailedMove({ key, message: errorText(cause) }))),
    ),
})

const SendKey = Command.define('SendKey', {
  args: { key: Schema.Literals(terminalKeys), homeName: Schema.String },
  messages: [Message.CompletedCommand, Message.ChangedTerminalStatus],
  execute: ({ key, homeName }) =>
    Effect.sync(() =>
      activeTerminal?.sendKey(key)
        ? Message.CompletedCommand()
        : Message.ChangedTerminalStatus({ text: `Not sent: ${homeName} is not connected.` }),
    ),
})

// iOS gives a web view no gesture that closes the keyboard, so Hide and a tap on the terminal carry one.
export const HideKeyboard = Command.define('HideKeyboard', {
  messages: [Message.CompletedCommand],
  execute: Effect.promise(async () => {
    ;(document.activeElement as HTMLElement | null)?.blur()
    if (Capacitor.isNativePlatform()) await Keyboard.hide()
    return Message.CompletedCommand()
  }),
})

export const PinchZoom = Mount.define('PinchZoom', {
  messages: [Message.AttachedZoom],
  execute: ({ element }) =>
    Effect.acquireRelease(
      Effect.sync(() => attachZoom(element)),
      (detach) => Effect.sync(detach),
    ).pipe(Effect.map(() => Message.AttachedZoom())),
})

// UPDATE

const startsStillBetweenSessions = (
  starts: Model['crewStarts'],
  pairingId: string,
  betweenSessions: ReadonlyArray<CrewMemberBetweenSessions>,
): Model['crewStarts'] => {
  const prefix = crewRowKey(pairingId, '')
  const stillBetweenSessions = new Set(
    betweenSessions.map((member) => crewRowKey(pairingId, member.crewMember)),
  )
  return Object.fromEntries(
    Object.entries(starts).filter(([key]) => !key.startsWith(prefix) || stillBetweenSessions.has(key)),
  )
}

const updateHome = (model: Model, pairingId: string, change: (home: Home) => Home): Model =>
  modifyFields(model, {
    homes: (homes) => homes.map((home) => (home.pairing.id === pairingId ? change(home) : home)),
  })

const emptyComposer: Composer = { text: '', sending: false, note: '' }

const composerKey = (open: Model['openSession']) => (open ? `${open.pairingId}/${open.sessionId}` : null)

const composerOf = (model: Model, key: string): Composer => model.composers[key] ?? emptyComposer

const withComposer = (model: Model, key: string, change: (composer: Composer) => Composer): Model => {
  const { [key]: _, ...others } = model.composers
  const next = change(composerOf(model, key))
  return modifyFields(model, {
    composers: () =>
      next.text === '' && !next.sending && next.note === '' ? others : { ...others, [key]: next },
  })
}

const newHome = (pairing: Pairing): Home => ({
  pairing,
  api: 0,
  link: 0,
  status: 'connecting',
  reason: '',
  sessions: [],
  crewBetweenSessions: [],
  listed: false,
  asks: [],
  garden: null,
})

const saved = (homes: ReadonlyArray<Home>) => SavePairings({ pairings: homes.map((home) => home.pairing) })

const openAskOf = (model: Model) => {
  const open = model.openAsk
  if (!open) return undefined
  return model.homes
    .find((home) => home.pairing.id === open.pairingId)
    ?.asks.find((ask) => ask.id === open.askId)
}

const openAskKey = (model: Model): string | null =>
  model.openAsk ? askKey(model.openAsk.pairingId, model.openAsk.askId) : null

const draftOf = (model: Model): Draft => {
  const key = openAskKey(model)
  return (key && model.drafts[key]) || emptyDraft
}

const answeringOpenAsk = (model: Model) => {
  const key = openAskKey(model)
  return key ? model.answering[key] : undefined
}

const without = <V>(record: Readonly<Record<string, V>>, key: string): Record<string, V> =>
  Object.fromEntries(Object.entries(record).filter(([k]) => k !== key))

const withDraft = (model: Model, change: (draft: Draft) => Draft): Model => {
  const key = openAskKey(model)
  if (!key || answeringOpenAsk(model)?.status === 'sending') return model
  return modifyFields(model, {
    drafts: (drafts) => ({ ...drafts, [key]: change(draftOf(model)) }),
    answering: (answering) => without(answering, key),
  })
}

const withAnswer = (model: Model, key: string, change: (answer: Answer) => Answer): Model =>
  withDraft(model, (draft) => ({
    ...draft,
    answers: { ...draft.answers, [key]: change(draft.answers[key] ?? emptyAnswer(key)) },
  }))

const sendDraft = (model: Model): Update.Return<Model, Message> => {
  const ask = openAskOf(model)
  const key = openAskKey(model)
  if (!ask || !model.openAsk || !key || answeringOpenAsk(model)?.status === 'sending') return { model }
  const draft = draftOf(model)
  return {
    model: modifyFields(model, {
      answering: (answering) => ({ ...answering, [key]: { status: 'sending' as const } }),
    }),
    commands: [
      SendAnswer({
        pairingId: model.openAsk.pairingId,
        askId: ask.id,
        answers: draftAnswers(ask, draft),
        note: draft.note.trim(),
      }),
    ],
  }
}

const askMarkdowns = (ask: PhoneAsk): ReadonlyArray<string> =>
  ask.parts.flatMap((part) => [part.markdown ?? '', part.question?.context ?? ''].filter(Boolean))

const openSeedOf = (model: Model) => model.seedTrail.at(-1)

const openSeedKey = (model: Model): string | null => {
  const open = openSeedOf(model)
  return open ? seedKey(open.pairingId, open.seedId) : null
}

export const seedKey = (pairingId: string, seedId: string) => `${pairingId}/${seedId}`

const seedMarkdowns = (model: Model): ReadonlyArray<string> =>
  model.screen === 'seed' && model.seedPage.status === 'loaded'
    ? [model.seedPage.detail.body, ...model.seedPage.detail.log.map((entry) => entry.body)]
    : []

const emptyNote: NoteDraft = { text: '', sending: false, error: '' }

const withNote = (model: Model, key: string, change: (note: NoteDraft) => NoteDraft): Model => {
  const { [key]: current, ...others } = model.seedNotes
  const next = change(current ?? emptyNote)
  return modifyFields(model, {
    seedNotes: () =>
      next.text === '' && !next.sending && next.error === '' ? others : { ...others, [key]: next },
  })
}

const showSeed = (model: Model, trail: Model['seedTrail']): Update.Return<Model, Message> => {
  const top = trail.at(-1)
  return {
    model: modifyFields(model, {
      screen: () => (top ? ('seed' as const) : ('garden' as const)),
      seedTrail: () => trail,
      seedPage: () => ({ status: 'loading' as const }),
      seedMove: () => null,
    }),
    commands: top ? [GetSeed(top)] : [],
  }
}

const refreshMentions = (model: Model): Update.Return<Model, Message> => {
  const open = model.openAsk
  const home = model.homes.find((entry) => entry.pairing.id === open?.pairingId)
  if (!open || !home || home.api < 9 || home.status !== 'online') return { model }
  const version = (model.mentionVersion ?? 0) + 1
  return {
    model: { ...model, mentionVersion: version, mentions: [], mentionError: '' },
    commands: [GetAskSeedMentions({ ...open, version })],
  }
}

const loadSeed = (model: Model, detail: SeedDetail): Update.Return<Model, Message> => {
  const shown = modifyFields(model, { seedPage: () => ({ status: 'loaded' as const, detail }) })
  keepParsed([...model.homes.flatMap((home) => home.asks.flatMap(askMarkdowns)), ...seedMarkdowns(shown)])
  const renders = [...new Set(visualsOf(seedMarkdowns(shown)).diagrams)].filter(
    (code) => !model.diagrams[code],
  )
  return {
    model: modifyFields(shown, {
      diagrams: (diagrams) => ({
        ...diagrams,
        ...Object.fromEntries(renders.map((code) => [code, { status: 'loading' as const }])),
      }),
    }),
    commands: renders.map((code) => RenderDiagram({ code })),
  }
}

const receiveGarden = (
  model: Model,
  pairingId: string,
  seeds: ReadonlyArray<GardenSeed>,
): Update.Return<Model, Message> => {
  const received = updateHome(model, pairingId, (home) => ({ ...home, garden: seeds }))
  const open = openSeedOf(model)
  const page = model.seedPage
  if (model.screen !== 'seed' || open?.pairingId !== pairingId || page.status !== 'loaded')
    return { model: received }
  const listed = seeds.find((seed) => seed.id === open.seedId)
  const wasListed = model.homes
    .find((home) => home.pairing.id === pairingId)
    ?.garden?.some((seed) => seed.id === open.seedId)
  const changed = listed ? JSON.stringify(listed) !== JSON.stringify(page.detail.seed) : wasListed === true
  return { model: received, commands: changed ? [GetSeed(open)] : [] }
}

const receiveAsks = (
  model: Model,
  pairingId: string,
  asks: ReadonlyArray<PhoneAsk>,
): Update.Return<Model, Message> => {
  const homes = model.homes.map((home) => (home.pairing.id === pairingId ? { ...home, asks } : home))
  keepParsed([...homes.flatMap((home) => home.asks.flatMap(askMarkdowns)), ...seedMarkdowns(model)])
  const listed = new Set(homes.flatMap((home) => home.asks.map((ask) => askKey(home.pairing.id, ask.id))))
  const ofListed = <V>(record: Readonly<Record<string, V>>) =>
    Object.fromEntries(Object.entries(record).filter(([key]) => listed.has(key)))
  const wantedImages = new Set<string>()
  const wantedDiagrams = new Set<string>()
  const loads: Array<{ pairingId: string; askId: string; src: string }> = []
  for (const home of homes) {
    for (const ask of home.asks) {
      const visuals = visualsOf(askMarkdowns(ask))
      for (const code of visuals.diagrams) wantedDiagrams.add(code)
    }
  }
  for (const code of visualsOf(seedMarkdowns(model)).diagrams) wantedDiagrams.add(code)
  for (const home of homes) {
    for (const ask of home.asks) {
      const visuals = visualsOf(askMarkdowns(ask))
      for (const src of visuals.images) {
        const key = imageKey(home.pairing.id, ask.id, src)
        wantedImages.add(key)
        const shown = model.images[key]
        const needed = !shown || (shown.status === 'failed' && shown.retry)
        if (home.pairing.id === pairingId && ask.reply === null && needed)
          loads.push({ pairingId, askId: ask.id, src })
      }
    }
  }
  const released = Object.entries(model.images).flatMap(([key, shown]) =>
    !wantedImages.has(key) && shown.status === 'loaded' ? [shown.url] : [],
  )
  const images: Record<string, ImageState> = Object.fromEntries(
    Object.entries(model.images).filter(([key]) => wantedImages.has(key)),
  )
  for (const load of loads) images[imageKey(load.pairingId, load.askId, load.src)] = { status: 'loading' }
  const diagrams = Object.fromEntries(
    Object.entries(model.diagrams).filter(([code]) => wantedDiagrams.has(code)),
  )
  const renders = [...wantedDiagrams].filter((code) => !diagrams[code])
  for (const code of renders) diagrams[code] = { status: 'loading' }
  return {
    model: modifyFields(model, {
      homes: () => homes,
      images: () => images,
      diagrams: () => diagrams,
      drafts: ofListed,
      answering: ofListed,
    }),
    commands: [
      ...loads.map((load) => LoadImage(load)),
      ...renders.map((code) => RenderDiagram({ code })),
      ...(released.length > 0 ? [ReleaseImages({ urls: released })] : []),
    ],
  }
}

export const update = (model: Model, message: Message): Update.Return<Model, Message> =>
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
          foreground
            ? homes.map((home) => ({ ...home, status: 'connecting' as const, reason: '', listed: false }))
            : homes.map((home) => ({ ...home, link: 0 })),
      }),
    }),
    StartedLink: ({ pairingId, link }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, link })),
    }),
    WelcomedByHome: ({ pairingId, api, homeName, profileName }) => {
      const welcomed = updateHome(model, pairingId, (home) => ({
        ...home,
        api,
        status: 'online',
        reason: '',
        pairing: { ...home.pairing, homeName, profileName },
      }))
      const renamed = model.homes.some(
        (home) =>
          home.pairing.id === pairingId &&
          (home.pairing.homeName !== homeName || home.pairing.profileName !== profileName),
      )
      const home = model.homes.find((home) => home.pairing.id === pairingId)
      const open = openSeedOf(model)
      return {
        model: welcomed,
        commands: [
          ...(renamed ? [saved(welcomed.homes)] : []),
          ...(home && model.pushToken ? registerWith([home], model.pushToken) : []),
          ...(model.screen === 'seed' && open?.pairingId === pairingId ? [GetSeed(open)] : []),
        ],
      }
    },
    ReceivedSessions: ({ pairingId, sessions, crewBetweenSessions = [] }) => ({
      model: modifyFields(
        updateHome(model, pairingId, (home) => ({
          ...home,
          status: 'online',
          reason: '',
          sessions,
          crewBetweenSessions,
          listed: true,
        })),
        { crewStarts: (starts) => startsStillBetweenSessions(starts, pairingId, crewBetweenSessions) },
      ),
    }),
    ReceivedAsks: ({ pairingId, asks }) => receiveAsks(model, pairingId, asks),
    ReceivedGarden: ({ pairingId, seeds }) => {
      const received = receiveGarden(model, pairingId, seeds)
      return model.screen === 'ask' && model.openAsk?.pairingId === pairingId
        ? refreshMentions(received.model)
        : received
    },
    ChangedSeed: ({ pairingId, seedId }) =>
      model.screen === 'ask' && model.openAsk?.pairingId === pairingId
        ? refreshMentions(model)
        : {
            model,
            commands:
              model.screen === 'seed' && openSeedKey(model) === seedKey(pairingId, seedId)
                ? [GetSeed({ pairingId, seedId })]
                : [],
          },
    ClickedGardenTab: () => ({
      model: modifyFields(model, {
        screen: () => 'garden' as const,
        seedTrail: () => [],
        seedMove: () => null,
        openAsk: () => null,
        zoom: () => null,
      }),
    }),
    ClickedSeed: ({ pairingId, seedId }) =>
      showSeed(model, [...(model.screen === 'ask' ? [] : model.seedTrail), { pairingId, seedId }]),
    LoadedMentions: ({ pairingId, askId, version, mentions }) => ({
      model:
        model.openAsk?.pairingId === pairingId &&
        model.openAsk.askId === askId &&
        model.mentionVersion === version
          ? modifyFields(model, { mentions: () => mentions, mentionError: () => '' })
          : model,
    }),
    FailedMentions: ({ pairingId, askId, version, message }) => ({
      model:
        model.openAsk?.pairingId === pairingId &&
        model.openAsk.askId === askId &&
        model.mentionVersion === version
          ? modifyFields(model, { mentions: () => [], mentionError: () => message })
          : model,
    }),
    LoadedSeed: ({ pairingId, seedId, detail }) =>
      openSeedKey(model) === seedKey(pairingId, seedId) ? loadSeed(model, detail) : { model },
    FailedSeed: ({ pairingId, seedId, message }) => ({
      model:
        openSeedKey(model) === seedKey(pairingId, seedId) && model.seedPage.status !== 'loaded'
          ? modifyFields(model, { seedPage: () => ({ status: 'failed' as const, message }) })
          : model,
    }),
    ClickedReloadSeed: () => showSeed(model, model.seedTrail),
    ChangedSeedNote: ({ value }) => {
      const key = openSeedKey(model)
      return key
        ? { model: withNote(model, key, (note) => ({ ...note, text: value, error: '' })) }
        : { model }
    },
    ClickedAddNote: () => {
      const open = openSeedOf(model)
      const key = openSeedKey(model)
      if (!open || !key) return { model }
      const note = model.seedNotes[key] ?? emptyNote
      if (note.sending || note.text.trim() === '') return { model }
      return {
        model: withNote(model, key, () => ({ ...note, sending: true, error: '' })),
        commands: [AddNote({ key, ...open, body: note.text.trim() })],
      }
    },
    AddedNote: ({ key, text }) => {
      const open = openSeedOf(model)
      const noted = withNote(model, key, (note) =>
        note.text.trim() === text ? emptyNote : { ...note, sending: false },
      )
      return { model: noted, commands: open && openSeedKey(model) === key ? [GetSeed(open)] : [] }
    },
    FailedNote: ({ key, message }) => ({
      model: withNote(model, key, (note) => ({ ...note, sending: false, error: `Not added: ${message}` })),
    }),
    ClickedMove: ({ verb }) => ({
      model: modifyFields(model, { seedMove: () => ({ verb, text: '', sending: false, error: '' }) }),
    }),
    ChangedMoveText: ({ value }) => ({
      model: modifyFields(model, {
        seedMove: (move) => (move && !move.sending ? { ...move, text: value, error: '' } : move),
      }),
    }),
    CancelledMove: () => ({
      model: modifyFields(model, { seedMove: (move) => (move?.sending ? move : null) }),
    }),
    ClickedConfirmMove: () => {
      const open = openSeedOf(model)
      const key = openSeedKey(model)
      const move = model.seedMove
      if (!open || !key || !move || move.sending || move.text.trim() === '') return { model }
      return {
        model: modifyFields(model, { seedMove: () => ({ ...move, sending: true, error: '' }) }),
        commands: [MoveSeed({ key, ...open, verb: move.verb, text: move.text.trim() })],
      }
    },
    MovedSeed: ({ key }) => {
      const open = openSeedOf(model)
      if (!open || openSeedKey(model) !== key) return { model }
      return { model: modifyFields(model, { seedMove: () => null }), commands: [GetSeed(open)] }
    },
    FailedMove: ({ key, message }) => ({
      model:
        openSeedKey(model) === key
          ? modifyFields(model, {
              seedMove: (move) => (move ? { ...move, sending: false, error: `Not moved: ${message}` } : move),
            })
          : model,
    }),
    ClickedAsk: ({ pairingId, askId }) =>
      refreshMentions({
        ...modifyFields(model, {
          screen: () => 'ask' as const,
          openAsk: () => ({ pairingId, askId }),
          seedTrail: () => [],
        }),
        mentions: [],
        mentionError: '',
      }),
    ClickedBack: () =>
      model.screen === 'seed'
        ? model.seedTrail.length === 1 && model.openAsk
          ? refreshMentions(modifyFields(model, { screen: () => 'ask' as const, seedTrail: () => [] }))
          : showSeed(model, model.seedTrail.slice(0, -1))
        : {
            model: modifyFields(model, {
              screen: () => 'queue' as const,
              openAsk: () => null,
              openSession: () => null,
              zoom: () => null,
            }),
            commands: model.screen === 'session' ? [HideKeyboard()] : [],
          },
    PickedChoice: ({ key, label }) => {
      const ask = openAskOf(model)
      const question = ask?.parts.find((part) => part.question?.key === key)?.question
      if (!ask || !question || answeringOpenAsk(model)?.status === 'sending') return { model }
      const picked = withAnswer(model, key, (answer) =>
        question.kind === 'one'
          ? { ...answer, picked: [label], other: '' }
          : {
              ...answer,
              picked: answer.picked.includes(label)
                ? answer.picked.filter((value) => value !== label)
                : [...answer.picked, label],
            },
      )
      return answersInOneTap(ask) ? sendDraft(picked) : { model: picked }
    },
    ChangedOther: ({ key, value }) => {
      const question = openAskOf(model)?.parts.find((part) => part.question?.key === key)?.question
      return {
        model: withAnswer(model, key, (answer) =>
          question?.kind === 'one' && value.trim() !== ''
            ? { ...answer, other: value, picked: [] }
            : { ...answer, other: value },
        ),
      }
    },
    ChangedText: ({ key, value }) => ({
      model: withAnswer(model, key, (answer) => ({ ...answer, text: value })),
    }),
    ChangedNote: ({ value }) => ({ model: withDraft(model, (draft) => ({ ...draft, note: value })) }),
    ClickedAcceptRecommended: () => {
      const ask = openAskOf(model)
      if (!ask || answeringOpenAsk(model)?.status === 'sending') return { model }
      return {
        model: withDraft(model, (draft) => ({
          ...draft,
          answers: {
            ...draft.answers,
            ...Object.fromEntries(recommendedAnswers(ask).map((answer) => [answer.key, answer])),
          },
        })),
      }
    },
    ClickedSend: () => sendDraft(model),
    SentAnswer: ({ pairingId, askId }) => {
      const key = askKey(pairingId, askId)
      const stillOpen = model.openAsk?.pairingId === pairingId && model.openAsk.askId === askId
      return {
        model: modifyFields(model, {
          answering: (answering) => without(answering, key),
          drafts: (drafts) => without(drafts, key),
          screen: () => (stillOpen ? ('queue' as const) : model.screen),
          openAsk: () => (stillOpen ? null : model.openAsk),
        }),
      }
    },
    FailedAnswer: ({ pairingId, askId, message }) => ({
      model: modifyFields(model, {
        answering: (answering) => ({
          ...answering,
          [askKey(pairingId, askId)]: { status: 'failed' as const, message: `Not sent: ${message}` },
        }),
      }),
    }),
    LoadedImage: ({ key, url }) =>
      model.images[key]
        ? {
            model: modifyFields(model, {
              images: (images) => ({ ...images, [key]: { status: 'loaded' as const, url } }),
            }),
          }
        : { model, commands: [ReleaseImages({ urls: [url] })] },
    FailedImage: ({ key, message, retry }) => ({
      model: model.images[key]
        ? modifyFields(model, {
            images: (images) => ({ ...images, [key]: { status: 'failed' as const, message, retry } }),
          })
        : model,
    }),
    RenderedDiagram: ({ code, svg, width }) => ({
      model: model.diagrams[code]
        ? modifyFields(model, {
            diagrams: (diagrams) => ({ ...diagrams, [code]: { status: 'rendered' as const, svg, width } }),
          })
        : model,
    }),
    FailedDiagram: ({ code, message }) => ({
      model: model.diagrams[code]
        ? modifyFields(model, {
            diagrams: (diagrams) => ({ ...diagrams, [code]: { status: 'failed' as const, message } }),
          })
        : model,
    }),
    ReleasedImages: () => ({ model }),
    ClickedZoomImage: ({ url, alt }) => ({
      model: modifyFields(model, { zoom: () => ({ kind: 'image' as const, url, alt }) }),
    }),
    ClickedZoomDiagram: ({ svg }) => ({
      model: modifyFields(model, { zoom: () => ({ kind: 'diagram' as const, svg }) }),
    }),
    ClosedZoom: () => ({ model: modifyFields(model, { zoom: () => null }) }),
    AttachedZoom: () => ({ model }),
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
    ClosedByHome: ({ pairingId, reason }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, status: 'closed', reason })),
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
    ClickedQueueTab: () => ({
      model: modifyFields(model, { screen: () => 'queue' as const, openAsk: () => null, zoom: () => null }),
    }),
    ClickedPairingsTab: () => ({ model: modifyFields(model, { screen: () => 'pairings' as const }) }),
    ClickedRemovePairing: ({ pairingId }) => {
      const homes = model.homes.filter((home) => home.pairing.id !== pairingId)
      return { model: modifyFields(model, { homes: () => homes }), commands: [saved(homes)] }
    },
    SavedPairings: () => ({ model }),
    Ticked: ({ now }) => ({ model: modifyFields(model, { now: () => now }) }),
    ClickedMemberBetweenSessions: ({ pairingId, crewMember }) => {
      const key = crewRowKey(pairingId, crewMember)
      return model.crewStarts[key]?.status === 'starting'
        ? { model }
        : {
            model: modifyFields(model, {
              crewStarts: (starts) => ({ ...starts, [key]: { status: 'starting' as const } }),
            }),
            commands: [StartCrewMember({ key, pairingId, crewMember })],
          }
    },
    AcceptedStart: ({ key }) => ({
      model: modifyFields(model, {
        crewStarts: (starts) => Object.fromEntries(Object.entries(starts).filter(([start]) => start !== key)),
      }),
    }),
    RefusedStart: ({ key, message }) => ({
      model: modifyFields(model, {
        crewStarts: (starts) => ({ ...starts, [key]: { status: 'refused' as const, message } }),
      }),
    }),
    ClickedSession: ({ pairingId, sessionId }) => ({
      model: modifyFields(model, {
        screen: () => 'session' as const,
        openSession: () => ({ pairingId, sessionId }),
        terminalStatus: () => '',
      }),
    }),
    ChangedComposer: ({ value }) => {
      const key = composerKey(model.openSession)
      return key
        ? { model: withComposer(model, key, (composer) => ({ ...composer, text: value })) }
        : { model }
    },
    SubmittedComposer: () => {
      const open = model.openSession
      const key = composerKey(open)
      if (!open || !key) return { model }
      const composer = composerOf(model, key)
      if (composer.sending || composer.text.trim() === '') return { model }
      return {
        model: withComposer(model, key, () => ({ ...composer, sending: true, note: '' })),
        commands: [SendPrompt({ key, ...open, text: composer.text })],
      }
    },
    DeliveredPrompt: ({ key, text }) => ({
      model: withComposer(model, key, (composer) =>
        composer.text === text ? emptyComposer : { ...composer, sending: false, note: '' },
      ),
    }),
    FailedPrompt: ({ key, message }) => ({
      model: withComposer(model, key, (composer) => ({ ...composer, sending: false, note: message })),
    }),
    ClickedKey: ({ key }) => {
      const home = model.homes.find((candidate) => candidate.pairing.id === model.openSession?.pairingId)
      return { model, commands: [SendKey({ key, homeName: home?.pairing.homeName ?? 'the home' })] }
    },
    ClickedHideKeyboard: () => ({ model, commands: [HideKeyboard()] }),
    TappedTerminal: () => ({ model, commands: model.keyboardHeight > 0 ? [HideKeyboard()] : [] }),
    ChangedKeyboard: ({ height }) => ({ model: modifyFields(model, { keyboardHeight: () => height }) }),
    ChangedViewport: ({ height }) => ({
      model: model.keyboardHeight === 0 ? modifyFields(model, { fullHeight: () => height }) : model,
    }),
    ChangedTerminalStatus: ({ text }) => ({ model: modifyFields(model, { terminalStatus: () => text }) }),
    CompletedCommand: () => ({ model }),
    ReceivedPushToken: ({ pushToken }) => ({
      model: modifyFields(model, { pushToken: () => pushToken, pushNote: () => '' }),
      commands: registerWith(
        model.homes.filter((home) => home.status === 'online'),
        pushToken,
      ),
    }),
    RefusedPush: ({ message }) => ({ model: modifyFields(model, { pushNote: () => message }) }),
    RegisteredPush: ({ homeKey }) => ({
      model: modifyFields(model, {
        pushRefusals: (refusals) =>
          Object.fromEntries(Object.entries(refusals).filter(([key]) => key !== homeKey)),
      }),
    }),
    FailedPush: ({ homeKey, message }) => ({
      model: modifyFields(model, { pushRefusals: (refusals) => ({ ...refusals, [homeKey]: message }) }),
    }),
    TappedNotification: ({ pairingId, sessionId, askId }) =>
      update(
        model,
        askId ? Message.ClickedAsk({ pairingId, askId }) : Message.ClickedSession({ pairingId, sessionId }),
      ),
  })

// INIT

export const init: Runtime.ApplicationInit<Model, Message> = () => ({
  model: {
    screen: 'queue',
    seedTrail: [],
    seedPage: { status: 'loading' },
    seedNotes: {},
    seedMove: null,
    openSession: null,
    composers: {},
    terminalStatus: '',
    fullHeight: window.innerHeight,
    keyboardHeight: 0,
    openAsk: null,
    drafts: {},
    answering: {},
    crewStarts: {},
    images: {},
    diagrams: {},
    zoom: null,
    loaded: false,
    foreground: document.visibilityState === 'visible',
    homes: [],
    pairingFlow: 'idle',
    pairingMessage: '',
    typedCode: '',
    pushToken: null,
    pushNote: '',
    pushRefusals: {},
    now: Date.now(),
  },
  commands: [LoadPairings()],
})

// SUBSCRIPTIONS

const linkMessage = (pairingId: string, event: LinkEvent): Message => {
  switch (event._tag) {
    case 'Welcomed':
      return Message.WelcomedByHome({
        pairingId,
        api: event.api,
        homeName: event.homeName,
        profileName: event.profileName,
      })
    case 'Sessions':
      return Message.ReceivedSessions({
        pairingId,
        sessions: event.sessions.map(fromHomeGatewaySession),
        crewBetweenSessions: event.crewBetweenSessions.map(fromHomeGatewayCrewMemberBetweenSessions),
      })
    case 'Asks':
      return Message.ReceivedAsks({ pairingId, asks: event.asks.map(fromHomeGatewayAsk) })
    case 'Garden':
      return Message.ReceivedGarden({ pairingId, seeds: event.seeds.map(fromHomeGatewaySeed) })
    case 'SeedChanged':
      return Message.ChangedSeed({ pairingId, seedId: event.seedId })
    case 'HomeOffline':
      return Message.WentOffline({ pairingId })
    case 'Lost':
      return Message.LostHome({ pairingId, reason: event.reason })
    case 'Closed':
      return Message.ClosedByHome({ pairingId, reason: event.reason })
  }
}

let linksStarted = 0

const linkStream = (pairing: LinkTarget) =>
  Stream.callback<Message>((queue) =>
    Effect.acquireRelease(
      Effect.promise(async () => {
        const keys = await phoneKeys()
        const link = runLink(pairing, keys, (event) =>
          Queue.offerUnsafe(queue, linkMessage(pairing.id, event)),
        )
        liveLinks.set(pairing.id, link)
        Queue.offerUnsafe(queue, Message.StartedLink({ pairingId: pairing.id, link: ++linksStarted }))
        return link
      }),
      (link) =>
        Effect.sync(() => {
          if (liveLinks.get(pairing.id) === link) liveLinks.delete(pairing.id)
          link.close()
        }),
    ).pipe(Effect.flatMap(() => Effect.never)),
  )

const notificationTapStream = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.promise(async () => {
      const { PushNotifications } = await import('@capacitor/push-notifications')
      return PushNotifications.addListener('pushNotificationActionPerformed', ({ notification }) => {
        const tap = readPushTap(notification.data)
        if (tap) {
          Queue.offerUnsafe(
            queue,
            Message.TappedNotification({
              pairingId: pairingId(tap.homeId, tap.profileId),
              sessionId: tap.sessionId,
              askId: tap.askId,
            }),
          )
        }
      })
    }),
    (handle) => Effect.promise(() => handle.remove()),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const pushTokenStream = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.promise(() =>
      watchPushTokens(
        (pushToken) => Queue.offerUnsafe(queue, Message.ReceivedPushToken({ pushToken })),
        (message) => Queue.offerUnsafe(queue, Message.RefusedPush({ message })),
      ).catch((error: unknown) => {
        Queue.offerUnsafe(
          queue,
          Message.RefusedPush({ message: `attn could not set up notifications: ${errorText(error)}` }),
        )
        return async () => {}
      }),
    ),
    (stop) => Effect.promise(stop),
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

const keyboardStream = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.promise(() =>
      Promise.all([
        Keyboard.addListener('keyboardWillShow', ({ keyboardHeight }) =>
          Queue.offerUnsafe(queue, Message.ChangedKeyboard({ height: keyboardHeight })),
        ),
        Keyboard.addListener('keyboardWillHide', () =>
          Queue.offerUnsafe(queue, Message.ChangedKeyboard({ height: 0 })),
        ),
      ]),
    ),
    (handles) => Effect.promise(() => Promise.all(handles.map((handle) => handle.remove()))),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const viewportStream = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.sync(() => {
      const resized = () => Queue.offerUnsafe(queue, Message.ChangedViewport({ height: window.innerHeight }))
      window.addEventListener('resize', resized)
      return resized
    }),
    (resized) => Effect.sync(() => window.removeEventListener('resize', resized)),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const tickStream = Stream.tick(Duration.seconds(30)).pipe(
  Stream.map(() => Message.Ticked({ now: Date.now() })),
)

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  links: entry(
    { connected: Schema.Array(LinkTarget) },
    {
      modelToDependencies: (model) => ({
        connected: model.foreground ? model.homes.map((home) => linkTarget(home.pairing)) : [],
      }),
      dependenciesToStream: ({ connected }) =>
        Stream.mergeAll(connected.map(linkStream), { concurrency: 'unbounded' }),
    },
  ),
  keyboard: entry(
    { on: Schema.Boolean },
    {
      modelToDependencies: () => ({ on: Capacitor.isNativePlatform() }),
      dependenciesToStream: ({ on }) => (on ? keyboardStream : Stream.empty),
    },
  ),
  viewport: entry(
    { on: Schema.Boolean },
    { modelToDependencies: () => ({ on: true }), dependenciesToStream: () => viewportStream },
  ),
  pushTokens: entry(
    { on: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        on: Capacitor.isNativePlatform() && model.foreground && model.homes.length > 0,
      }),
      dependenciesToStream: ({ on }) => (on ? pushTokenStream : Stream.empty),
    },
  ),
  notificationTaps: entry(
    { on: Schema.Boolean },
    {
      modelToDependencies: () => ({ on: Capacitor.isNativePlatform() }),
      dependenciesToStream: ({ on }) => (on ? notificationTapStream : Stream.empty),
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
          model.foreground &&
          (model.screen === 'seed' ||
            model.homes.some(
              (home) => home.asks.length > 0 || home.sessions.some((s) => s.waitingSince !== null),
            )),
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

const sessionRow = (row: SessionRow, now: number, h: HtmlBuilder<Message>, title?: Html): Html =>
  h.li(
    [
      h.Key(row.key),
      h.Class(`row session state-${row.session.state}${row.live ? '' : ' offline'}`),
      h.DataAttribute('testid', 'queue-entry'),
      ...(row.live
        ? [h.OnClick(Message.ClickedSession({ pairingId: row.pairing.id, sessionId: row.session.id }))]
        : []),
    ],
    [
      h.div(
        [h.Class('row-main')],
        [
          title ?? h.div([h.Class('row-title')], [row.session.title || row.session.id]),
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

const crewTitle = (name: string, chief: boolean, h: HtmlBuilder<Message>): Html =>
  h.div(
    [h.Class('row-title')],
    [name, ...(chief && name.toLowerCase() !== 'chief' ? [h.span([h.Class('crew-chief')], ['Chief'])] : [])],
  )

const crewSessionRow = (row: SessionRow, now: number, h: HtmlBuilder<Message>): Html =>
  sessionRow(
    row,
    now,
    h,
    crewTitle(row.session.crewMemberName ?? row.session.title ?? row.session.id, row.session.chief, h),
  )

const betweenSessionsRow = (
  row: BetweenSessionsRow,
  start: CrewStart | undefined,
  h: HtmlBuilder<Message>,
): Html => {
  const tappable = row.live && start?.status !== 'starting'
  const badge = !row.live ? 'offline' : start?.status === 'starting' ? 'starting…' : 'between sessions'
  return h.li(
    [
      h.Key(row.key),
      h.Class(
        `row session crew-between-sessions${row.live ? '' : ' offline'}${start?.status === 'starting' ? ' starting' : ''}`,
      ),
      h.DataAttribute('testid', 'queue-entry'),
      ...(tappable
        ? [
            h.OnClick(
              Message.ClickedMemberBetweenSessions({
                pairingId: row.pairing.id,
                crewMember: row.member.crewMember,
              }),
            ),
          ]
        : []),
    ],
    [
      h.div(
        [h.Class('row-main')],
        [
          crewTitle(row.member.name, row.member.chief, h),
          h.div([h.Class('row-where')], [where(row.pairing, null)]),
          row.live && start?.status === 'refused'
            ? h.div([h.Class('row-where error')], [start.message])
            : h.empty,
        ],
      ),
      h.div([h.Class('row-side')], [h.span([h.Class('badge')], [badge])]),
    ],
  )
}

const homeStatusLabel: Record<HomeRow['status'], string> = {
  connecting: 'connecting…',
  home_offline: 'offline',
  unreachable: 'unreachable',
  closed: 'not allowed',
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
      h.div(
        [h.Class('row-side')],
        [
          h.span([h.Class('badge')], [homeStatusLabel[row.status]]),
          h.button(
            [h.Class('quiet'), h.OnClick(Message.ClickedRemovePairing({ pairingId: row.pairing.id }))],
            ['Forget'],
          ),
        ],
      ),
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

const askActions = (model: Model): AskActions<Message> => ({
  image: (src) => imageView(model, src),
  diagram: (code): DiagramView => model.diagrams[code] ?? { status: 'loading' },
  zoomImage: (url, alt) => Message.ClickedZoomImage({ url, alt }),
  zoomDiagram: (svg) => Message.ClickedZoomDiagram({ svg }),
  openAsk: (pairingId, askId) => Message.ClickedAsk({ pairingId, askId }),
  openSeed: (pairingId, seedId) => Message.ClickedSeed({ pairingId, seedId }),
  back: Message.ClickedBack(),
  pick: (key, label) => Message.PickedChoice({ key, label }),
  changeOther: (key, value) => Message.ChangedOther({ key, value }),
  changeText: (key, value) => Message.ChangedText({ key, value }),
  changeNote: (value) => Message.ChangedNote({ value }),
  acceptRecommended: Message.ClickedAcceptRecommended(),
  send: Message.ClickedSend(),
})

const gardenActions = (model: Model): GardenActions<Message> => ({
  image: () => ({ status: 'failed', message: 'Images in a seed open on your computer.' }),
  diagram: (code): DiagramView => model.diagrams[code] ?? { status: 'loading' },
  zoomImage: (url, alt) => Message.ClickedZoomImage({ url, alt }),
  zoomDiagram: (svg) => Message.ClickedZoomDiagram({ svg }),
  openSeed: (pairingId, seedId) => Message.ClickedSeed({ pairingId, seedId }),
  reload: Message.ClickedReloadSeed(),
  changeNote: (value) => Message.ChangedSeedNote({ value }),
  sendNote: Message.ClickedAddNote(),
  chooseMove: (verb) => Message.ClickedMove({ verb }),
  changeMoveText: (value) => Message.ChangedMoveText({ value }),
  cancelMove: Message.CancelledMove(),
  confirmMove: Message.ClickedConfirmMove(),
})

const gardenView = (model: Model, h: HtmlBuilder<Message>): Html => {
  if (!model.loaded) return h.div([h.Class('empty')], [])
  if (model.homes.length === 0) return pairHint(h)
  return gardenScreen(
    model.homes.map((home) => ({
      pairing: home.pairing,
      seeds: home.garden,
      status: home.status,
      reason: home.reason,
      tooOld: home.api > 0 && home.api < homeGardenApi,
    })),
    gardenActions(model),
    h,
  )
}

const openSeedView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const open = openSeedOf(model)
  const home = model.homes.find((candidate) => candidate.pairing.id === open?.pairingId)
  if (!open || !home) return h.p([h.Class('empty muted')], ['This seed is gone.'])
  const key = seedKey(open.pairingId, open.seedId)
  return seedScreen(
    {
      pairing: home.pairing,
      live: home.status === 'online',
      page: model.seedPage,
      note: model.seedNotes[key] ?? emptyNote,
      move: model.seedMove,
      now: model.now,
    },
    waitedFor,
    gardenActions(model),
    h,
  )
}

const imageView = (model: Model, src: string): ImageView => {
  const open = model.openAsk
  const shown = open ? model.images[imageKey(open.pairingId, open.askId, src)] : undefined
  if (!shown || shown.status === 'loading') return { status: 'loading' }
  return shown.status === 'loaded' ? shown : { status: 'failed', message: shown.message }
}

const askRowsOf = (home: Home): ReadonlyArray<AskRow> =>
  home.asks.map((ask) => ({
    key: askKey(home.pairing.id, ask.id),
    ask,
    pairing: home.pairing,
    from: askFrom(ask, home.sessions.find((session) => session.id === ask.sessionId)?.title),
    live: home.status === 'online',
  }))

export const askRows = (homes: ReadonlyArray<Home>): ReadonlyArray<AskRow> =>
  homes
    .flatMap(askRowsOf)
    .filter((row) => row.ask.reply === null)
    .toSorted((a, b) => a.ask.createdAt - b.ask.createdAt || a.key.localeCompare(b.key))

const queueView = (model: Model, h: HtmlBuilder<Message>): Html => {
  if (!model.loaded) return h.div([h.Class('empty')], [])
  if (model.homes.length === 0) return pairHint(h)
  const queue = buildQueue(model.homes)
  const asks = askRows(model.homes)
  const actions = askActions(model)
  const sessions = (rows: ReadonlyArray<SessionRow>) => rows.map((row) => sessionRow(row, model.now, h))
  const entries =
    queue.crew.length +
    asks.length +
    queue.waiting.length +
    queue.working.length +
    queue.rest.length +
    queue.offline.length
  const nothing = entries === 0 && model.homes.every((home) => home.listed)
  return h.div(
    [h.Class('queue')],
    [
      nothing ? h.p([h.Class('empty muted')], ['No agents yet.']) : h.empty,
      section(
        'Crew',
        queue.crew.map((row) =>
          row.kind === 'betweenSessions'
            ? betweenSessionsRow(row, model.crewStarts[row.key], h)
            : crewSessionRow(row, model.now, h),
        ),
        h,
      ),
      section(
        'Asks',
        asks.map((row) => askRow(row, waitedFor(row.ask.createdAt, model.now), actions, h)),
        h,
      ),
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

const pushNotesView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const refused = model.homes.flatMap(({ pairing }, index, homes) => {
    const key = pushHomeKey(pairing.cloud, pairing.homeId)
    const message = model.pushRefusals[key]
    const first = homes.findIndex((home) => pushHomeKey(home.pairing.cloud, home.pairing.homeId) === key)
    return message && first === index ? [`${pairing.homeName}: ${message}`] : []
  })
  const notes = [...(model.pushNote ? [model.pushNote] : []), ...refused]
  return notes.length === 0
    ? h.empty
    : h.div(
        [h.Class('push-notes')],
        notes.map((note) => h.p([h.Class('error push-note')], [note])),
      )
}

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

// MOUNTS

export const TerminalMount = Mount.defineStream('TerminalMount', {
  args: { pairingId: Schema.String, terminalId: Schema.String, link: Schema.Finite },
  messages: [Message.ChangedTerminalStatus, Message.TappedTerminal],
  execute: ({ element, pairingId, terminalId }) =>
    Stream.callback<typeof Message.ChangedTerminalStatus.Type | typeof Message.TappedTerminal.Type>((queue) =>
      Effect.acquireRelease(
        Effect.sync(() => {
          const link = liveLinks.get(pairingId)
          const status = (text: string) => Queue.offerUnsafe(queue, Message.ChangedTerminalStatus({ text }))
          if (!link) {
            status('This home is not connected.')
            return () => {}
          }
          return mountTerminal(element as HTMLElement, link, terminalId, status, () =>
            Queue.offerUnsafe(queue, Message.TappedTerminal()),
          )
        }),
        (dispose) => Effect.sync(dispose),
      ).pipe(Effect.flatMap(() => Effect.never)),
    ),
})

// A tap on a key or on Send must not take focus from the composer, or iOS closes the keyboard.
export const KeepFocus = Mount.define('KeepFocus', {
  messages: [Message.CompletedCommand],
  execute: ({ element }) =>
    Effect.gen(function* () {
      yield* Effect.acquireRelease(
        Effect.sync(() => {
          const keep = (event: Event) => event.preventDefault()
          element.addEventListener('pointerdown', keep)
          element.addEventListener('mousedown', keep)
          return keep
        }),
        (keep) =>
          Effect.sync(() => {
            element.removeEventListener('pointerdown', keep)
            element.removeEventListener('mousedown', keep)
          }),
      )
      return Message.CompletedCommand()
    }),
})

const keyLabels: Record<TerminalKey, string> = {
  Esc: 'Esc',
  Up: '↑',
  Down: '↓',
  Enter: '⏎',
  '1': '1',
  '2': '2',
  '3': '3',
  Tab: 'Tab',
  CtrlC: '^C',
  ShiftTab: '⇧Tab',
}

const keysBar = (live: boolean, keyboardOpen: boolean, h: HtmlBuilder<Message>): Html =>
  h.div(
    [h.Class('keys'), h.OnMount(KeepFocus())],
    [
      keyboardOpen
        ? h.button([h.Class('hide-keyboard'), h.OnClick(Message.ClickedHideKeyboard())], ['Hide'])
        : h.empty,
      ...terminalKeys.map((key) =>
        h.button([h.Key(key), h.Disabled(!live), h.OnClick(Message.ClickedKey({ key }))], [keyLabels[key]]),
      ),
    ],
  )

const composerView = (composer: Composer, live: boolean, h: HtmlBuilder<Message>): Html =>
  h.div(
    [h.Class('composer-area')],
    [
      composer.note ? h.p([h.Class('composer-note error')], [composer.note]) : h.empty,
      h.form(
        [h.Class('composer'), h.OnSubmit(Message.SubmittedComposer())],
        [
          h.textarea([
            h.Value(composer.text),
            h.OnInput((value) => Message.ChangedComposer({ value })),
            h.Placeholder('Reply to the agent…'),
            h.Rows(1),
            h.Attribute('enterkeyhint', 'send'),
            h.Attribute('autocapitalize', 'sentences'),
            h.Attribute('autocorrect', 'on'),
            h.Spellcheck(true),
            h.OnKeyDownPreventDefault((key, mods) =>
              key === 'Enter' && !mods.shiftKey ? Option.some(Message.SubmittedComposer()) : Option.none(),
            ),
          ]),
          h.button(
            [
              h.Type('submit'),
              h.Class('primary'),
              h.Disabled(composer.sending || !live),
              h.OnMount(KeepFocus()),
            ],
            [composer.sending ? 'Sending…' : 'Send'],
          ),
        ],
      ),
    ],
  )

const unlistedSessionText = (model: Model, home: Home | undefined): string => {
  if (!home) return model.loaded ? 'This session is gone.' : 'Connecting…'
  if (home.listed) return 'This session is gone.'
  return home.status === 'connecting' || home.status === 'online'
    ? `Connecting to ${home.pairing.homeName}…`
    : home.reason
}

const sessionView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const open = model.openSession
  const home = model.homes.find((candidate) => candidate.pairing.id === open?.pairingId)
  const session = home?.sessions.find((candidate) => candidate.id === open?.sessionId)
  const back = h.button([h.Class('back'), h.OnClick(Message.ClickedBack())], ['‹ Queue'])
  const live = home?.status === 'online' && !session?.offline
  if (!open || !home || !session) {
    return h.div(
      [h.Class('session-screen')],
      [
        h.header([h.Class('session-top')], [back]),
        h.p([h.Class('empty muted')], [unlistedSessionText(model, home)]),
      ],
    )
  }
  const connecting = home.status === 'connecting' && !session.offline
  const header = h.header(
    [h.Class('session-top')],
    [
      back,
      h.div(
        [h.Class('session-title')],
        [
          session.crewMemberName
            ? crewTitle(session.crewMemberName, session.chief, h)
            : h.div([h.Class('row-title')], [session.title || session.id]),
          h.div(
            [h.Class('row-where')],
            [
              `${live ? stateLabel[session.state] : connecting ? 'connecting…' : 'offline'} · ${where(home.pairing, session.host)}`,
            ],
          ),
        ],
      ),
    ],
  )
  if (home.api > 0 && home.api < homeTerminalApi) {
    return h.div(
      [h.Class('session-screen')],
      [
        header,
        h.p(
          [h.Class('empty muted')],
          [
            `attn on ${home.pairing.homeName} is too old to open terminals from a phone. Update it to the latest attn.`,
          ],
        ),
      ],
    )
  }
  return h.div(
    [h.Class('session-screen')],
    [
      header,
      !session.terminalId
        ? h.div([h.Class('terminal-host empty muted')], ['This session has no terminal.'])
        : home.link === 0
          ? h.div([h.Class('terminal-host empty muted')], [`Connecting to ${home.pairing.homeName}…`])
          : h.keyed('div')(
              `${open.pairingId}/${session.terminalId}`,
              [
                h.Class('terminal-host'),
                h.OnMount(
                  TerminalMount({
                    pairingId: open.pairingId,
                    terminalId: session.terminalId,
                    link: home.link,
                  }),
                ),
              ],
              [],
            ),
      live || connecting
        ? h.empty
        : h.p(
            [h.Class('terminal-status error'), h.DataAttribute('testid', 'home-offline')],
            [
              `${home.reason || `${home.pairing.homeName} is not connected`}. Keys and Send work again when it is back.`,
            ],
          ),
      model.terminalStatus ? h.p([h.Class('terminal-status muted')], [model.terminalStatus]) : h.empty,
      keysBar(live, model.keyboardHeight > 0, h),
      composerView(composerOf(model, composerKey(open)!), live, h),
    ],
  )
}

const waitingCount = (model: Model) => waitingAgents(buildQueue(model.homes)) + askRows(model.homes).length

const openAskView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const answering = answeringOpenAsk(model)
  const open = model.openAsk
  const home = open ? model.homes.find((home) => home.pairing.id === open.pairingId) : undefined
  const row = home && open ? askRowsOf(home).find((row) => row.ask.id === open.askId) : undefined
  return askScreen(
    {
      row,
      draft: draftOf(model),
      sending: answering?.status === 'sending',
      error: answering?.status === 'failed' ? answering.message : (model.mentionError ?? ''),
      mentions: model.mentions ?? [],
    },
    model.now,
    waitedFor,
    askActions(model),
    h,
  )
}

const zoomView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const zoom = model.zoom
  if (!zoom) return h.empty
  return h.div(
    [h.Class('zoom'), h.Role('dialog'), h.AriaModal(true), h.DataAttribute('testid', 'zoom')],
    [
      h.div(
        [h.Class('zoom-surface'), h.OnMount(PinchZoom())],
        [
          zoom.kind === 'image'
            ? h.img([h.Class('zoom-content'), h.Src(zoom.url), h.Alt(zoom.alt)])
            : h.div([h.Class('zoom-content zoom-diagram'), h.InnerHTML(zoom.svg)], []),
        ],
      ),
      h.button(
        [h.Class('zoom-close'), h.Type('button'), h.AriaLabel('Close'), h.OnClick(Message.ClosedZoom())],
        ['Done'],
      ),
    ],
  )
}

const screenTitle = {
  queue: 'Queue',
  pairings: 'Pairings',
  ask: 'Ask',
  session: 'Session',
  garden: 'Garden',
  seed: 'Seed',
} as const

const backLabel = (model: Model): string | null => {
  if (model.screen === 'ask') return '‹ Queue'
  if (model.screen !== 'seed') return null
  const parent = model.seedTrail.at(-2)
  if (!parent) return model.openAsk ? '‹ Ask' : '‹ Garden'
  const shown = model.homes
    .find((home) => home.pairing.id === parent.pairingId)
    ?.garden?.find((seed) => seed.id === parent.seedId)
  return `‹ ${shown?.title ?? 'Back'}`
}

const screenView = (model: Model, h: HtmlBuilder<Message>): Html => {
  switch (model.screen) {
    case 'queue':
      return queueView(model, h)
    case 'ask':
      return openAskView(model, h)
    case 'garden':
      return gardenView(model, h)
    case 'seed':
      return openSeedView(model, h)
    default:
      return pairingsView(model, h)
  }
}

export const view = (model: Model, h: HtmlBuilder<Message>): Document => ({
  title: 'attn',
  body:
    model.screen === 'session'
      ? h.div(
          [h.Class('app'), h.Style({ height: `${Math.round(model.fullHeight - model.keyboardHeight)}px` })],
          [sessionView(model, h)],
        )
      : h.div(
          [h.Class('app')],
          [
            h.header(
              [h.Class('top')],
              [
                backLabel(model)
                  ? h.button(
                      [h.Class('back'), h.Type('button'), h.OnClick(Message.ClickedBack())],
                      [backLabel(model)],
                    )
                  : h.h1([], [screenTitle[model.screen]]),
                model.screen === 'queue' && waitingCount(model) > 0
                  ? h.span([h.Class('waiting-count')], [`${waitingCount(model)} waiting`])
                  : h.empty,
              ],
            ),
            h.main(
              [],
              [
                model.screen === 'ask' || model.screen === 'seed' ? h.empty : pushNotesView(model, h),
                screenView(model, h),
              ],
            ),
            zoomView(model, h),
            h.nav(
              [h.Class('tabs')],
              [
                h.button(
                  [
                    h.Class(model.screen === 'queue' || model.screen === 'ask' ? 'on' : ''),
                    h.OnClick(Message.ClickedQueueTab()),
                  ],
                  ['Queue'],
                ),
                h.button(
                  [
                    h.Class(model.screen === 'garden' || model.screen === 'seed' ? 'on' : ''),
                    h.OnClick(Message.ClickedGardenTab()),
                  ],
                  ['Garden'],
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
