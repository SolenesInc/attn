import { App } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'
import { Duration, Effect, Queue, Schema, Stream } from 'effect'
import { renderMermaid } from '@attn/components/Markdown/mermaidRender'
import { Command, Mount, Runtime, Subscription, type Update } from 'foldkit'
import { type Document, type Html, type HtmlBuilder } from 'foldkit/html'
import { defineMessageUnion } from 'foldkit/message'
import { modifyFields } from 'foldkit/struct'
import {
  PhoneAsk,
  Draft,
  answersInOneTap,
  askFrom,
  askKey,
  draftAnswers,
  emptyAnswer,
  emptyDraft,
  fromGatewayAsk,
  type Answer,
} from './asks/ask'
import { ImageRefused, fetchAskImage } from './asks/images'
import { visualsOf, type DiagramView, type ImageView } from './asks/markdown'
import { askRow, askScreen, type AskActions, type AskRow } from './asks/view'
import { attachZoom } from './asks/zoom'
import { fromBase64Url } from './bytes'
import { deviceKeys } from './device'
import { pairWithHome, runLink, type Link, type LinkEvent } from './link'
import {
  LinkTarget,
  Pairing,
  linkTarget,
  loadPairings,
  pairingFromOffer,
  readOffer,
  savePairings,
} from './pairing'
import { Home, QueueSession, buildQueue, fromGatewaySession, type HomeRow, type SessionRow } from './queue'

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
  Schema.Struct({ status: Schema.Literal('rendered'), svg: Schema.String }),
  Schema.Struct({ status: Schema.Literal('failed'), message: Schema.String }),
])

const Zoom = Schema.Union([
  Schema.Struct({ kind: Schema.Literal('image'), url: Schema.String, alt: Schema.String }),
  Schema.Struct({ kind: Schema.Literal('diagram'), svg: Schema.String }),
])

const OpenAskId = Schema.Struct({ pairingId: Schema.String, askId: Schema.String })

export const Model = Schema.Struct({
  screen: Schema.Literals(['queue', 'pairings', 'ask']),
  openAsk: Schema.NullOr(OpenAskId),
  drafts: Schema.Record(Schema.String, Draft),
  sending: Schema.Boolean,
  sendError: Schema.String,
  images: Schema.Record(Schema.String, ImageState),
  diagrams: Schema.Record(Schema.String, DiagramState),
  zoom: Schema.NullOr(Zoom),
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
  WelcomedByHome: { pairingId: Schema.String, homeName: Schema.String, profileName: Schema.String },
  ReceivedSessions: { pairingId: Schema.String, sessions: Schema.Array(QueueSession) },
  ReceivedAsks: { pairingId: Schema.String, asks: Schema.Array(PhoneAsk) },
  ClickedAsk: { pairingId: Schema.String, askId: Schema.String },
  ClickedBack: {},
  PickedChoice: { key: Schema.String, label: Schema.String },
  ChangedOther: { key: Schema.String, value: Schema.String },
  ChangedText: { key: Schema.String, value: Schema.String },
  ChangedNote: { value: Schema.String },
  ClickedSend: {},
  SentAnswer: { pairingId: Schema.String, askId: Schema.String },
  FailedAnswer: { pairingId: Schema.String, askId: Schema.String, message: Schema.String },
  LoadedImage: { key: Schema.String, url: Schema.String },
  FailedImage: { key: Schema.String, message: Schema.String, retry: Schema.Boolean },
  RenderedDiagram: { code: Schema.String, svg: Schema.String },
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
      return pairingFromOffer(offer, welcome.homeName, welcome.profileName, Date.now())
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

export const RenderDiagram = Command.define('RenderDiagram', {
  args: { code: Schema.String },
  messages: [Message.RenderedDiagram, Message.FailedDiagram],
  execute: ({ code }) =>
    Effect.tryPromise(() =>
      renderMermaid(
        `ask-diagram-${diagramCount++}`,
        code,
        window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'neutral',
      ),
    ).pipe(
      Effect.map((svg) => Message.RenderedDiagram({ code, svg })),
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

export const PinchZoom = Mount.define('PinchZoom', {
  messages: [Message.AttachedZoom],
  execute: ({ element }) =>
    Effect.acquireRelease(
      Effect.sync(() => attachZoom(element)),
      (detach) => Effect.sync(detach),
    ).pipe(Effect.map(() => Message.AttachedZoom())),
})

// UPDATE

const updateHome = (model: Model, pairingId: string, change: (home: Home) => Home): Model =>
  modifyFields(model, {
    homes: (homes) => homes.map((home) => (home.pairing.id === pairingId ? change(home) : home)),
  })

const newHome = (pairing: Pairing): Home => ({
  pairing,
  status: 'connecting',
  reason: '',
  sessions: [],
  asks: [],
})

const saved = (homes: ReadonlyArray<Home>) => SavePairings({ pairings: homes.map((home) => home.pairing) })

const openAskOf = (model: Model) => {
  const open = model.openAsk
  if (!open) return undefined
  return model.homes
    .find((home) => home.pairing.id === open.pairingId)
    ?.asks.find((ask) => ask.id === open.askId)
}

const draftOf = (model: Model): Draft =>
  (model.openAsk && model.drafts[askKey(model.openAsk.pairingId, model.openAsk.askId)]) || emptyDraft

const withDraft = (model: Model, change: (draft: Draft) => Draft): Model => {
  if (!model.openAsk) return model
  const key = askKey(model.openAsk.pairingId, model.openAsk.askId)
  return modifyFields(model, {
    drafts: (drafts) => ({ ...drafts, [key]: change(draftOf(model)) }),
    sendError: () => '',
  })
}

const withAnswer = (model: Model, key: string, change: (answer: Answer) => Answer): Model =>
  withDraft(model, (draft) => ({
    ...draft,
    answers: { ...draft.answers, [key]: change(draft.answers[key] ?? emptyAnswer(key)) },
  }))

const sendDraft = (model: Model): Update.Return<Model, Message> => {
  const ask = openAskOf(model)
  if (!ask || !model.openAsk || model.sending) return { model }
  const draft = draftOf(model)
  return {
    model: modifyFields(model, { sending: () => true, sendError: () => '' }),
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

// Keeps the images and diagrams of the asks the homes still list, and starts loading the open asks' missing ones.
const receiveAsks = (
  model: Model,
  pairingId: string,
  asks: ReadonlyArray<PhoneAsk>,
): Update.Return<Model, Message> => {
  const homes = model.homes.map((home) => (home.pairing.id === pairingId ? { ...home, asks } : home))
  const wantedImages = new Set<string>()
  const wantedDiagrams = new Set<string>()
  const loads: Array<{ pairingId: string; askId: string; src: string }> = []
  for (const home of homes) {
    for (const ask of home.asks) {
      const visuals = visualsOf(askMarkdowns(ask))
      for (const code of visuals.diagrams) wantedDiagrams.add(code)
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
    model: modifyFields(model, { homes: () => homes, images: () => images, diagrams: () => diagrams }),
    commands: [
      ...loads.map((load) => LoadImage(load)),
      ...renders.map((code) => RenderDiagram({ code })),
      ...(released.length > 0 ? [ReleaseImages({ urls: released })] : []),
    ],
  }
}

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
    WelcomedByHome: ({ pairingId, homeName, profileName }) => {
      const welcomed = updateHome(model, pairingId, (home) => ({
        ...home,
        status: 'online',
        reason: '',
        pairing: { ...home.pairing, homeName, profileName },
      }))
      const renamed = model.homes.some(
        (home) =>
          home.pairing.id === pairingId &&
          (home.pairing.homeName !== homeName || home.pairing.profileName !== profileName),
      )
      return { model: welcomed, commands: renamed ? [saved(welcomed.homes)] : [] }
    },
    ReceivedSessions: ({ pairingId, sessions }) => ({
      model: updateHome(model, pairingId, (home) => ({ ...home, status: 'online', reason: '', sessions })),
    }),
    ReceivedAsks: ({ pairingId, asks }) => receiveAsks(model, pairingId, asks),
    ClickedAsk: ({ pairingId, askId }) => ({
      model: modifyFields(model, {
        screen: () => 'ask' as const,
        openAsk: () => ({ pairingId, askId }),
        sending: () => false,
        sendError: () => '',
      }),
    }),
    ClickedBack: () => ({
      model: modifyFields(model, {
        screen: () => 'queue' as const,
        openAsk: () => null,
        sendError: () => '',
        zoom: () => null,
      }),
    }),
    PickedChoice: ({ key, label }) => {
      const ask = openAskOf(model)
      const question = ask?.parts.find((part) => part.question?.key === key)?.question
      if (!ask || !question || model.sending) return { model }
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
    ClickedSend: () => sendDraft(model),
    SentAnswer: ({ pairingId, askId }) => {
      const key = askKey(pairingId, askId)
      const stillOpen = model.openAsk?.pairingId === pairingId && model.openAsk.askId === askId
      return {
        model: modifyFields(model, {
          sending: () => false,
          drafts: (drafts) => Object.fromEntries(Object.entries(drafts).filter(([k]) => k !== key)),
          screen: () => (stillOpen ? ('queue' as const) : model.screen),
          openAsk: () => (stillOpen ? null : model.openAsk),
        }),
      }
    },
    FailedAnswer: ({ message }) => ({
      model: modifyFields(model, { sending: () => false, sendError: () => `Not sent: ${message}` }),
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
    RenderedDiagram: ({ code, svg }) => ({
      model: model.diagrams[code]
        ? modifyFields(model, {
            diagrams: (diagrams) => ({ ...diagrams, [code]: { status: 'rendered' as const, svg } }),
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
  })

// INIT

export const init: Runtime.ApplicationInit<Model, Message> = () => ({
  model: {
    screen: 'queue',
    openAsk: null,
    drafts: {},
    sending: false,
    sendError: '',
    images: {},
    diagrams: {},
    zoom: null,
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
      return Message.WelcomedByHome({ pairingId, homeName: event.homeName, profileName: event.profileName })
    case 'Sessions':
      return Message.ReceivedSessions({ pairingId, sessions: event.sessions.map(fromGatewaySession) })
    case 'Asks':
      return Message.ReceivedAsks({ pairingId, asks: event.asks.map(fromGatewayAsk) })
    case 'HomeOffline':
      return Message.WentOffline({ pairingId })
    case 'Lost':
      return Message.LostHome({ pairingId, reason: event.reason })
    case 'Closed':
      return Message.ClosedByHome({ pairingId, reason: event.reason })
  }
}

const linkStream = (pairing: LinkTarget) =>
  Stream.callback<Message>((queue) =>
    Effect.acquireRelease(
      Effect.promise(async () => {
        const keys = await deviceKeys()
        const link = runLink(pairing, keys, (event) =>
          Queue.offerUnsafe(queue, linkMessage(pairing.id, event)),
        )
        liveLinks.set(pairing.id, link)
        return link
      }),
      (link) =>
        Effect.sync(() => {
          if (liveLinks.get(pairing.id) === link) liveLinks.delete(pairing.id)
          link.close()
        }),
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
    { connected: Schema.Array(LinkTarget) },
    {
      modelToDependencies: (model) => ({
        connected: model.foreground ? model.homes.map((home) => linkTarget(home.pairing)) : [],
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
          model.foreground &&
          model.homes.some(
            (home) => home.asks.length > 0 || home.sessions.some((s) => s.waitingSince !== null),
          ),
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

const askActions = (model: Model): AskActions<Message> => ({
  image: (src) => imageView(model, src),
  diagram: (code): DiagramView => model.diagrams[code] ?? { status: 'loading' },
  zoomImage: (url, alt) => Message.ClickedZoomImage({ url, alt }),
  zoomDiagram: (svg) => Message.ClickedZoomDiagram({ svg }),
  openAsk: (pairingId, askId) => Message.ClickedAsk({ pairingId, askId }),
  back: Message.ClickedBack(),
  pick: (key, label) => Message.PickedChoice({ key, label }),
  changeOther: (key, value) => Message.ChangedOther({ key, value }),
  changeText: (key, value) => Message.ChangedText({ key, value }),
  changeNote: (value) => Message.ChangedNote({ value }),
  send: Message.ClickedSend(),
})

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
  const nothing =
    asks.length + queue.waiting.length + queue.working.length + queue.rest.length + queue.offline.length === 0
  return h.div(
    [h.Class('queue')],
    [
      nothing ? h.p([h.Class('empty muted')], ['No agents yet.']) : h.empty,
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

const waitingCount = (model: Model) => buildQueue(model.homes).waiting.length + askRows(model.homes).length

const openAskView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const open = model.openAsk
  const home = open ? model.homes.find((home) => home.pairing.id === open.pairingId) : undefined
  const row = home && open ? askRowsOf(home).find((row) => row.ask.id === open.askId) : undefined
  return askScreen(
    { row, draft: draftOf(model), sending: model.sending, error: model.sendError },
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

const screenTitle = { queue: 'Queue', pairings: 'Pairings', ask: 'Ask' } as const

export const view = (model: Model, h: HtmlBuilder<Message>): Document => ({
  title: 'attn',
  body: h.div(
    [h.Class('app')],
    [
      h.header(
        [h.Class('top')],
        [
          model.screen === 'ask'
            ? h.button([h.Class('back'), h.Type('button'), h.OnClick(Message.ClickedBack())], ['‹ Queue'])
            : h.h1([], [screenTitle[model.screen]]),
          model.screen === 'queue' && waitingCount(model) > 0
            ? h.span([h.Class('waiting-count')], [`${waitingCount(model)} waiting`])
            : h.empty,
        ],
      ),
      h.main(
        [],
        [
          model.screen === 'queue'
            ? queueView(model, h)
            : model.screen === 'ask'
              ? openAskView(model, h)
              : pairingsView(model, h),
        ],
      ),
      zoomView(model, h),
      h.nav(
        [h.Class('tabs')],
        [
          h.button(
            [h.Class(model.screen !== 'pairings' ? 'on' : ''), h.OnClick(Message.ClickedQueueTab())],
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
