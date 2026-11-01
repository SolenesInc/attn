import { Effect, Option, Queue, Schema, Stream } from 'effect'
import { Command, Mount, Runtime, Subscription, type Update } from 'foldkit'
import { Document, Html, HtmlBuilder } from 'foldkit/html'
import { defineMessageUnion } from 'foldkit/message'
import { modifyFields } from 'foldkit/struct'
import { Keyboard } from '@capacitor/keyboard'
import { Capacitor } from '@capacitor/core'
import { daemon, toSpikeSession, type SpikeSession } from './daemon'
import { activeTerminal, mountTerminal, type SpecialKey } from './terminal'
import { counters, mark } from './metrics'

declare const __ATTN_WS_URL__: string
declare const __ATTN_CLIENT_TOKEN__: string

// MODEL

const Session = Schema.Struct({
  id: Schema.String,
  label: Schema.String,
  state: Schema.String,
  agent: Schema.String,
  directory: Schema.String,
})

const InputMode = Schema.Literals(['direct', 'composer'])
const SizeMode = Schema.Literals(['fit', 'native'])

export const Model = Schema.Struct({
  screen: Schema.Literals(['connect', 'sessions', 'terminal']),
  wsUrl: Schema.String,
  token: Schema.String,
  connection: Schema.Literals(['idle', 'connecting', 'connected', 'failed']),
  error: Schema.String,
  sessions: Schema.Array(Session),
  sessionId: Schema.String,
  inputMode: InputMode,
  sizeMode: SizeMode,
  fontSize: Schema.Number,
  composer: Schema.String,
  ctrlLatched: Schema.Boolean,
  keyboardHeight: Schema.Number,
  viewportHeight: Schema.Number,
  status: Schema.String,
  showLog: Schema.Boolean,
  logVersion: Schema.Number,
})
export type Model = typeof Model.Type

// MESSAGE

export const Message = defineMessageUnion({
  ChangedWsUrl: { value: Schema.String },
  ChangedToken: { value: Schema.String },
  ClickedConnect: {},
  SucceededConnect: { sessions: Schema.Array(Session) },
  FailedConnect: { error: Schema.String },
  ChangedSessions: { sessions: Schema.Array(Session) },
  ChangedSession: { session: Session },
  LostConnection: { reason: Schema.String },
  ClickedSession: { id: Schema.String },
  ClickedBack: {},
  ClickedInputMode: { mode: InputMode },
  ClickedSizeMode: { mode: SizeMode },
  ClickedFont: { delta: Schema.Number },
  ClickedKey: { key: Schema.String },
  ClickedCtrl: {},
  ChangedComposer: { value: Schema.String },
  SubmittedComposer: {},
  ClickedToggleLog: {},
  ClickedHideKeyboard: {},
  ChangedKeyboard: { height: Schema.Number },
  ChangedViewport: { height: Schema.Number },
  ChangedTerminalStatus: { text: Schema.String },
  LoggedInput: {},
  CompletedCommand: {},
  RenderedSessionList: {},
})
export type Message = typeof Message.Type

// COMMANDS

const Connect = Command.define('Connect', {
  args: { url: Schema.String, token: Schema.String },
  messages: [Message.SucceededConnect, Message.FailedConnect],
  execute: ({ url, token }) =>
    Effect.tryPromise(() => daemon.connect(url, token)).pipe(
      Effect.map((sessions) => Message.SucceededConnect({ sessions })),
      Effect.catch((e) => Effect.succeed(Message.FailedConnect({ error: String((e as any).cause ?? e) }))),
    ),
})

const SendKey = Command.define('SendKey', {
  args: { key: Schema.String },
  messages: [Message.CompletedCommand],
  execute: ({ key }) => Effect.sync(() => {
    activeTerminal?.sendKey(key as SpecialKey)
    return Message.CompletedCommand()
  }),
})

const SendCtrlChar = Command.define('SendCtrlChar', {
  args: { char: Schema.String },
  messages: [Message.CompletedCommand],
  execute: ({ char }) => Effect.sync(() => {
    const code = char.toLowerCase().charCodeAt(0) - 96
    if (code >= 1 && code <= 26) daemon.input(daemon.terminalBySession.get(currentSessionId) ?? currentSessionId, String.fromCharCode(code))
    return Message.CompletedCommand()
  }),
})

const LatchCtrl = Command.define('LatchCtrl', {
  args: { latched: Schema.Boolean },
  messages: [Message.CompletedCommand],
  execute: ({ latched }) => Effect.sync(() => {
    activeTerminal?.setCtrlLatched(latched)
    return Message.CompletedCommand()
  }),
})

const SubmitPrompt = Command.define('SubmitPrompt', {
  args: { text: Schema.String },
  messages: [Message.CompletedCommand],
  execute: ({ text }) => Effect.promise(async () => {
    await activeTerminal?.submit(text)
    return Message.CompletedCommand()
  }),
})

// iOS gives a WKWebView no gesture that closes the keyboard, so the keys bar carries one.
const HideKeyboard = Command.define('HideKeyboard', {
  messages: [Message.CompletedCommand],
  execute: Effect.promise(async () => {
    ;(document.activeElement as HTMLElement | null)?.blur()
    if (Capacitor.isNativePlatform()) await Keyboard.hide()
    return Message.CompletedCommand()
  }),
})

const MarkSessionListRendered = Command.define('MarkSessionListRendered', {
  messages: [Message.RenderedSessionList],
  execute: Effect.promise(() => new Promise<void>((r) => requestAnimationFrame(() => r()))).pipe(
    Effect.map(() => {
      mark('first_session_list_rendered')
      return Message.RenderedSessionList()
    }),
  ),
})

let currentSessionId = ''

// UPDATE

const STORAGE_KEY = 'attn-spike-connection'

export const update = (model: Model, message: Message) =>
  Message.match<Update.Return<Model, Message>>(message, {
    ChangedWsUrl: ({ value }) => ({ model: modifyFields(model, { wsUrl: () => value }) }),
    ChangedToken: ({ value }) => ({ model: modifyFields(model, { token: () => value }) }),
    ClickedConnect: () => {
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ wsUrl: model.wsUrl, token: model.token })) } catch { /* private mode */ }
      return {
        model: modifyFields(model, { connection: () => 'connecting' as const, error: () => '' }),
        commands: [Connect({ url: model.wsUrl, token: model.token })],
      }
    },
    SucceededConnect: ({ sessions }) => ({
      model: modifyFields(model, { connection: () => 'connected' as const, sessions: () => sessions, screen: () => 'sessions' as const }),
      commands: [MarkSessionListRendered()],
    }),
    FailedConnect: ({ error }) => ({ model: modifyFields(model, { connection: () => 'failed' as const, error: () => error }) }),
    ChangedSessions: ({ sessions }) => ({ model: modifyFields(model, { sessions: () => sessions }) }),
    ChangedSession: ({ session }) => ({
      model: modifyFields(model, {
        sessions: (all) => all.some((s) => s.id === session.id) ? all.map((s) => s.id === session.id ? session : s) : [...all, session],
      }),
    }),
    LostConnection: ({ reason }) => ({
      model: modifyFields(model, { connection: () => 'failed' as const, error: () => `connection lost: ${reason}`, screen: () => 'connect' as const }),
    }),
    ClickedSession: ({ id }) => {
      currentSessionId = id
      return { model: modifyFields(model, { sessionId: () => id, screen: () => 'terminal' as const, status: () => 'attaching…' }) }
    },
    ClickedBack: () => ({ model: modifyFields(model, { screen: () => 'sessions' as const, sessionId: () => '' }) }),
    ClickedInputMode: ({ mode }) => ({ model: modifyFields(model, { inputMode: () => mode }) }),
    ClickedSizeMode: ({ mode }) => ({ model: modifyFields(model, { sizeMode: () => mode }) }),
    ClickedFont: ({ delta }) => ({ model: modifyFields(model, { fontSize: (f) => Math.max(8, Math.min(20, f + delta)) }) }),
    ClickedKey: ({ key }) => ({ model, commands: [SendKey({ key })] }),
    ClickedCtrl: () => ({
      model: modifyFields(model, { ctrlLatched: (c) => !c }),
      commands: [LatchCtrl({ latched: !model.ctrlLatched })],
    }),
    ChangedComposer: ({ value }) => {
      if (model.ctrlLatched && value.length === model.composer.length + 1) {
        return {
          model: modifyFields(model, { ctrlLatched: () => false }),
          commands: [SendCtrlChar({ char: value.slice(-1) }), LatchCtrl({ latched: false })],
        }
      }
      return { model: modifyFields(model, { composer: () => value }) }
    },
    SubmittedComposer: () => ({
      model: modifyFields(model, { composer: () => '' }),
      commands: [SubmitPrompt({ text: model.composer })],
    }),
    ClickedHideKeyboard: () => ({ model, commands: [HideKeyboard()] }),
    ClickedToggleLog: () => ({ model: modifyFields(model, { showLog: (s) => !s }) }),
    ChangedKeyboard: ({ height }) => ({ model: modifyFields(model, { keyboardHeight: () => height }) }),
    ChangedViewport: ({ height }) => ({ model: modifyFields(model, { viewportHeight: () => height }) }),
    ChangedTerminalStatus: ({ text }) => ({ model: modifyFields(model, { status: () => text }) }),
    LoggedInput: () => ({ model: modifyFields(model, { logVersion: (v) => v + 1 }) }),
    CompletedCommand: () => ({ model }),
    RenderedSessionList: () => ({ model }),
  })

// INIT

export const init: Runtime.ApplicationInit<Model, Message> = () => {
  let saved: { wsUrl?: string; token?: string } = {}
  try { saved = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') } catch { /* none */ }
  const params = new URLSearchParams(location.search)
  const wsUrl = params.get('ws') ?? (__ATTN_WS_URL__ || saved.wsUrl || 'ws://localhost:28889/ws')
  const token = params.get('token') ?? (__ATTN_CLIENT_TOKEN__ || saved.token || '')
  const autoConnect = token !== ''
  return {
    model: {
      screen: 'connect',
      wsUrl,
      token,
      connection: autoConnect ? 'connecting' : 'idle',
      error: '',
      sessions: [],
      sessionId: '',
      inputMode: (params.get('input') as 'direct' | 'composer') ?? 'composer',
      sizeMode: (params.get('size') as 'fit' | 'native') ?? 'native',
      fontSize: 13,
      composer: '',
      ctrlLatched: false,
      keyboardHeight: 0,
      viewportHeight: window.visualViewport?.height ?? window.innerHeight,
      status: '',
      showLog: false,
      logVersion: 0,
    },
    commands: autoConnect ? [Connect({ url: wsUrl, token })] : [],
  }
}

// SUBSCRIPTIONS

const daemonEvents = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.sync(() => daemon.onEvent((e) => {
      if (e.event === 'sessions_updated' && e.sessions) Queue.offerUnsafe(queue, Message.ChangedSessions({ sessions: e.sessions.map(toSpikeSession) }))
      else if ((e.event === 'session_state_changed' || e.event === 'session_registered') && e.session) Queue.offerUnsafe(queue, Message.ChangedSession({ session: toSpikeSession(e.session) }))
      else if (e.event === '__closed') Queue.offerUnsafe(queue, Message.LostConnection({ reason: `${e.code} ${e.reason ?? ''}` }))
    })),
    (off) => Effect.sync(off),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const keyboardEvents = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.promise(async () => {
      if (!Capacitor.isNativePlatform()) return [] as Array<{ remove: () => Promise<void> }>
      return Promise.all([
        Keyboard.addListener('keyboardWillShow', (info) => Queue.offerUnsafe(queue, Message.ChangedKeyboard({ height: info.keyboardHeight }))),
        Keyboard.addListener('keyboardWillHide', () => Queue.offerUnsafe(queue, Message.ChangedKeyboard({ height: 0 }))),
      ])
    }),
    (handles) => Effect.promise(() => Promise.all(handles.map((h) => h.remove()))),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const viewportEvents = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.sync(() => {
      const vv = window.visualViewport
      const onResize = () => Queue.offerUnsafe(queue, Message.ChangedViewport({ height: vv?.height ?? window.innerHeight }))
      vv?.addEventListener('resize', onResize)
      window.addEventListener('resize', onResize)
      return () => { vv?.removeEventListener('resize', onResize); window.removeEventListener('resize', onResize) }
    }),
    (off) => Effect.sync(off),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

const inputLogEvents = Stream.callback<Message>((queue) =>
  Effect.acquireRelease(
    Effect.sync(() => {
      const onLog = () => Queue.offerUnsafe(queue, Message.LoggedInput())
      const onUnlatch = () => Queue.offerUnsafe(queue, Message.ClickedCtrl())
      document.addEventListener('spike-input-log', onLog)
      document.addEventListener('spike-ctrl-unlatched', onUnlatch)
      return () => { document.removeEventListener('spike-input-log', onLog); document.removeEventListener('spike-ctrl-unlatched', onUnlatch) }
    }),
    (off) => Effect.sync(off),
  ).pipe(Effect.flatMap(() => Effect.never)),
)

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  daemon: entry({ connected: Schema.Boolean }, {
    modelToDependencies: (m) => ({ connected: m.connection === 'connected' }),
    dependenciesToStream: ({ connected }) => connected ? daemonEvents : Stream.empty,
  }),
  keyboard: entry({ on: Schema.Boolean }, {
    modelToDependencies: () => ({ on: true }),
    dependenciesToStream: () => keyboardEvents,
  }),
  viewport: entry({ on: Schema.Boolean }, {
    modelToDependencies: () => ({ on: true }),
    dependenciesToStream: () => viewportEvents,
  }),
  inputLog: entry({ showLog: Schema.Boolean }, {
    modelToDependencies: (m) => ({ showLog: m.showLog }),
    dependenciesToStream: ({ showLog }) => showLog ? inputLogEvents : Stream.empty,
  }),
}))

// MOUNTS

const TerminalMount = Mount.defineStream('TerminalMount', {
  args: { sessionId: Schema.String, sizeMode: SizeMode, inputMode: InputMode, fontSize: Schema.Number },
  messages: [Message.ChangedTerminalStatus],
  execute: ({ element, sessionId, sizeMode, inputMode, fontSize }) =>
    Stream.callback<typeof Message.ChangedTerminalStatus.Type>((queue) =>
      Effect.acquireRelease(
        Effect.sync(() => mountTerminal(element as HTMLElement, { sessionId, sizeMode, inputMode, fontSize }, (text) =>
          Queue.offerUnsafe(queue, Message.ChangedTerminalStatus({ text })))),
        (dispose) => Effect.sync(dispose),
      ).pipe(Effect.flatMap(() => Effect.never)),
    ),
})

const KeepInputFocus = Mount.define('KeepInputFocus', {
  messages: [Message.CompletedCommand],
  execute: ({ element }) => Effect.gen(function* () {
    yield* Effect.acquireRelease(
      Effect.sync(() => {
        const keep = (e: Event) => e.preventDefault()
        element.addEventListener('pointerdown', keep)
        element.addEventListener('mousedown', keep)
        return keep
      }),
      (keep) => Effect.sync(() => {
        element.removeEventListener('pointerdown', keep)
        element.removeEventListener('mousedown', keep)
      }),
    )
    return Message.CompletedCommand()
  }),
})

// VIEW

const KEYS: Array<[label: string, key: string]> = [
  ['Esc', 'Esc'], ['↑', 'Up'], ['↓', 'Down'], ['⏎', 'Enter'], ['1', '1'], ['2', '2'], ['3', '3'],
  ['Tab', 'Tab'], ['^C', 'CtrlC'], ['⇧Tab', 'ShiftTab'], ['←', 'Left'], ['→', 'Right'],
]

const stateClass = (state: string) => `state state-${state}`

const connectView = (model: Model, h: HtmlBuilder<Message>): Html =>
  h.div([h.Class('connect')], [
    h.h1([], ['attn phone spike']),
    h.label([], ['Daemon WebSocket']),
    h.input([h.Value(model.wsUrl), h.OnInput((value) => Message.ChangedWsUrl({ value })), h.Autocomplete('off'), h.Spellcheck(false)]),
    h.label([], ['client_token']),
    h.input([h.Value(model.token), h.OnInput((value) => Message.ChangedToken({ value })), h.Autocomplete('off'), h.Spellcheck(false)]),
    h.button([h.OnClick(Message.ClickedConnect()), h.Disabled(model.connection === 'connecting')], [model.connection === 'connecting' ? 'Connecting…' : 'Connect']),
    model.error ? h.p([h.Class('error')], [model.error]) : h.empty,
  ])

const sessionsView = (model: Model, h: HtmlBuilder<Message>): Html =>
  h.div([h.Class('sessions')], [
    h.header([], [h.h1([], ['Sessions']), h.span([h.Class('muted')], [`${model.sessions.length}`])]),
    h.ul([], model.sessions.map((s) =>
      h.li([h.Key(s.id), h.OnClick(Message.ClickedSession({ id: s.id }))], [
        h.span([h.Class(stateClass(s.state))], [s.state.replace('_', ' ')]),
        h.div([h.Class('session-text')], [
          h.div([h.Class('session-label')], [s.label || s.id]),
          h.div([h.Class('muted')], [`${s.agent} · ${s.directory}`]),
        ]),
      ]))),
    h.footer([h.Class('modes')], modeButtons(model, h)),
  ])

const modeButtons = (model: Model, h: HtmlBuilder<Message>): Html[] => [
  h.button([h.Class(model.inputMode === 'direct' ? 'on' : ''), h.OnClick(Message.ClickedInputMode({ mode: 'direct' }))], ['(a) direct']),
  h.button([h.Class(model.inputMode === 'composer' ? 'on' : ''), h.OnClick(Message.ClickedInputMode({ mode: 'composer' }))], ['(b) composer']),
  h.button([h.Class(model.sizeMode === 'fit' ? 'on' : ''), h.OnClick(Message.ClickedSizeMode({ mode: 'fit' }))], ['fit PTY']),
  h.button([h.Class(model.sizeMode === 'native' ? 'on' : ''), h.OnClick(Message.ClickedSizeMode({ mode: 'native' }))], ['desktop size']),
]

const keysBar = (model: Model, h: HtmlBuilder<Message>): Html =>
  h.div([h.Class('keys'), h.OnMount(KeepInputFocus())], [
    h.button([h.Attribute('aria-label', 'Hide keyboard'), h.OnClick(Message.ClickedHideKeyboard())], ['⌄']),
    ...KEYS.map(([label, key]) => h.button([h.Key(key), h.OnClick(Message.ClickedKey({ key }))], [label])),
    h.button([h.Class(model.ctrlLatched ? 'on' : ''), h.OnClick(Message.ClickedCtrl())], ['Ctrl']),
  ])

const composerView = (model: Model, h: HtmlBuilder<Message>): Html =>
  h.form([h.Class('composer'), h.OnSubmit(Message.SubmittedComposer())], [
    h.textarea([
      h.Value(model.composer),
      h.OnInput((value) => Message.ChangedComposer({ value })),
      h.Placeholder('Reply to the agent…'),
      h.Rows(1),
      h.Attribute('enterkeyhint', 'send'),
      h.Attribute('autocapitalize', 'sentences'),
      h.Attribute('autocorrect', 'on'),
      h.Spellcheck(true),
      h.OnKeyDownPreventDefault((key, mods) =>
        key === 'Enter' && !mods.shiftKey ? Option.some(Message.SubmittedComposer()) : Option.none()),
    ]),
    h.button([h.Type('submit')], ['Send']),
  ])

const terminalView = (model: Model, h: HtmlBuilder<Message>): Html => {
  const session = model.sessions.find((s) => s.id === model.sessionId)
  const mountKey = `${model.sessionId}:${model.sizeMode}:${model.inputMode}:${model.fontSize}`
  return h.div([h.Class('terminal-screen')], [
    h.header([], [
      h.button([h.OnClick(Message.ClickedBack())], ['‹']),
      h.div([h.Class('title')], [
        h.div([h.Class('session-label')], [session?.label ?? model.sessionId]),
        h.div([h.Class('muted')], [`${session?.state ?? ''} · ${model.status} · kb ${model.keyboardHeight}px · vv ${Math.round(model.viewportHeight)}px`]),
      ]),
      h.button([h.OnClick(Message.ClickedFont({ delta: -1 }))], ['A−']),
      h.button([h.OnClick(Message.ClickedFont({ delta: 1 }))], ['A+']),
      h.button([h.Class(model.showLog ? 'on' : ''), h.OnClick(Message.ClickedToggleLog())], ['log']),
    ]),
    h.div([h.Class('modes')], modeButtons(model, h)),
    h.keyed('div')(mountKey, [h.Class('terminal-host'), h.OnMount(TerminalMount({
      sessionId: model.sessionId, sizeMode: model.sizeMode, inputMode: model.inputMode, fontSize: model.fontSize,
    }))], []),
    model.showLog ? h.pre([h.Class('input-log')], [counters.inputLog.slice(-12).join('\n')]) : h.empty,
    keysBar(model, h),
    model.inputMode === 'composer' ? composerView(model, h) : h.empty,
  ])
}

export const view = (model: Model, h: HtmlBuilder<Message>): Document => ({
  title: 'attn spike',
  body: h.div([h.Class('app'), h.Style({ height: `calc(${Math.round(model.viewportHeight)}px - env(safe-area-inset-top))` })], [
    model.screen === 'connect' ? connectView(model, h)
      : model.screen === 'sessions' ? sessionsView(model, h)
        : terminalView(model, h),
  ]),
})
