import { concatBytes } from '@noble/hashes/utils.js'
import { decodeHomeGatewayEvent, type HomeGatewayEvent } from '@attn/homeGateway/decode'
import type {
  HomeGatewayAsk,
  HomeGatewaySeed,
  HomeGatewayCrewMemberBetweenSessions,
  HomeGatewaySession,
  TerminalScreen,
} from '@attn/types/homeGatewayGenerated'
import { HOME_GATEWAY_API_VERSION } from '@attn/types/homeGatewayVersion'
import { fromBase64, fromBase64Url, toBase64Url } from './bytes'
import { relayToken, signChallenge, type PhoneKeys } from './phoneKeys'
import { HandshakeKind, startHandshake, type Transport } from './noise'

export const connectTimeoutMs = 10_000
export const pingIntervalMs = 15_000
export const pongDeadlineMs = 10_000
const homePingApi = 2
export const homeTerminalApi = 4
export const homeGardenApi = 6
const outputFrame = 0x01
const firstRetryMs = 100
const maxRetryMs = 10_000

const FrameType = {
  challenge: 1,
  auth: 2,
  ready: 3,
  data: 5,
  presence: 8,
} as const

const envelopeVersion = 1
const headerLength = 6

const encodeFrame = (type: number, connId: number, payload: Uint8Array): Uint8Array<ArrayBuffer> => {
  const frame = new Uint8Array(headerLength + payload.length)
  const view = new DataView(frame.buffer)
  view.setUint8(0, envelopeVersion)
  view.setUint8(1, type)
  view.setUint32(2, connId)
  frame.set(payload, headerLength)
  return frame
}

const decodeFrame = (data: Uint8Array) => {
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength)
  return { type: view.getUint8(1), connId: view.getUint32(2), payload: data.subarray(headerLength) }
}

export type Home = {
  readonly cloud: string
  readonly homeId: string
  readonly homeNoisePub: string
  readonly homeName: string
  readonly profileId: string
}

export type LinkEvent =
  | {
      readonly _tag: 'Welcomed'
      readonly api: number
      readonly homeName: string
      readonly profileName: string
    }
  | {
      readonly _tag: 'Sessions'
      readonly sessions: ReadonlyArray<HomeGatewaySession>
      readonly crewBetweenSessions: ReadonlyArray<HomeGatewayCrewMemberBetweenSessions>
    }
  | { readonly _tag: 'Asks'; readonly asks: ReadonlyArray<HomeGatewayAsk> }
  | { readonly _tag: 'Garden'; readonly seeds: ReadonlyArray<HomeGatewaySeed> }
  | { readonly _tag: 'SeedChanged'; readonly seedId: string }
  | { readonly _tag: 'HomeOffline' }
  | { readonly _tag: 'Lost'; readonly reason: string }
  | { readonly _tag: 'Closed'; readonly reason: string }

type Pairing = { readonly pairingId: string; readonly secret: Uint8Array }

export type Reply = Extract<HomeGatewayEvent, { request_id: string }>

// A request is one Home Gateway command that the home answers with one event carrying its request_id.
type Socket = {
  readonly close: () => void
  readonly request: (command: { readonly cmd: string; readonly [field: string]: unknown }) => Promise<Reply>
  readonly send: (message: object) => boolean
}

export type TerminalSink = {
  readonly screen: (screen: TerminalScreen, ansi: Uint8Array) => void
  readonly output: (data: Uint8Array) => void
  readonly resized: (cols: number, rows: number) => void
  readonly ended: () => void
  readonly failed: (message: string) => void
}

type TerminalEvent =
  | { readonly _tag: 'Output'; readonly terminalId: string; readonly data: Uint8Array }
  | { readonly _tag: 'Screen'; readonly screen: TerminalScreen; readonly ansi: Uint8Array }
  | { readonly _tag: 'Resized'; readonly terminalId: string; readonly cols: number; readonly rows: number }
  | { readonly _tag: 'Ended'; readonly terminalId: string }
  | { readonly _tag: 'Failed'; readonly terminalId: string; readonly message: string }

const decodeOutputFrame = (plaintext: Uint8Array): TerminalEvent => {
  const idLength = plaintext[1] ?? 0
  const terminalId = new TextDecoder().decode(plaintext.subarray(2, 2 + idLength))
  return { _tag: 'Output', terminalId, data: plaintext.subarray(2 + idLength + 4) }
}

export type TerminalRequest = { readonly cols: number; readonly rows: number }

export type OpenTerminal = {
  readonly send: (message: object) => boolean
  readonly grant: (bytes: number) => void
  readonly resize: (request: TerminalRequest) => void
  readonly close: () => void
}

export type Link = Omit<Socket, 'send'> & {
  readonly openTerminal: (terminalId: string, request: TerminalRequest, sink: TerminalSink) => OpenTerminal
}

// The credit a phone grants each terminal: four Cloud Relay messages in flight, and always room for a whole screen.
export const terminalCreditBytes = 256 << 10

type Pending = { readonly resolve: (reply: Reply) => void; readonly reject: (error: Error) => void }

const phoneAddress = (home: Home): string =>
  `${home.cloud.replace(/^http/, 'ws')}/v1/homes/${home.homeId}/device`

const closeReason = (
  home: Home,
  url: string,
  event: CloseEvent,
  ready: boolean,
  pairing: boolean,
): string => {
  if (pairing && event.code === 4403)
    return `this pairing code no longer works; show a new one in Settings → Phones on ${home.homeName}`
  switch (event.code) {
    case 4000:
      return `${home.homeName} ended this pairing; to use it again, pair from Settings → Phones`
    case 4403:
      return `${home.homeName} no longer allows this phone; pair it again from Settings → Phones`
    case 4400:
      return `the Cloud Relay refused a malformed frame: ${event.reason}`
    case 4401:
      return 'the Cloud Relay refused this phone’s signature'
    default:
      return ready
        ? `the connection to ${home.homeName} dropped (close code ${event.code})`
        : `could not reach ${home.homeName} at ${url} (close code ${event.code})`
  }
}

const openSocket = (
  home: Home,
  keys: PhoneKeys,
  pairing: Pairing | undefined,
  onEvent: (event: LinkEvent) => void,
  onTerminal: (event: TerminalEvent) => void,
  onEnd: (reason: string, final: boolean) => void,
): Socket => {
  const url = phoneAddress(home)
  const socket = new WebSocket(`${url}?device_sign_pub=${toBase64Url(keys.signPub)}`)
  socket.binaryType = 'arraybuffer'
  let ended = false
  let connId = 0
  let finishHandshake: ((reply: Uint8Array) => Promise<Transport>) | undefined
  let transport: Transport | undefined
  let refusal: string | undefined
  let homeAnswersPing = false
  let welcomed = false
  const pending = new Map<string, Pending>()
  const asks = new Map<string, HomeGatewayAsk>()
  const seeds = new Map<string, HomeGatewaySeed>()
  let deadline: ReturnType<typeof setTimeout> | undefined
  let pongDeadline: ReturnType<typeof setTimeout> | undefined

  const armDeadline = () => {
    clearTimeout(deadline)
    deadline = setTimeout(
      () => end(`${home.homeName} did not answer at ${url} within ${connectTimeoutMs / 1000} s`),
      connectTimeoutMs,
    )
  }
  const heartbeat = setInterval(() => {
    if (pongDeadline !== undefined) return
    const pinged = transport && homeAnswersPing ? home.homeName : `the Cloud Relay for ${home.homeName}`
    pongDeadline = setTimeout(
      () => end(`${pinged}: no answer for ${pongDeadlineMs / 1000} s; reconnecting`),
      pongDeadlineMs,
    )
    if (transport && homeAnswersPing) inOrder(() => sendHomeGateway({ cmd: 'ping' }))
    else socket.send('ping')
  }, pingIntervalMs)
  const answered = () => {
    clearTimeout(pongDeadline)
    pongDeadline = undefined
  }

  const failPending = (reason: string) => {
    for (const request of pending.values()) request.reject(new Error(reason))
    pending.clear()
  }
  const end = (reason: string, final = false) => {
    if (ended) return
    ended = true
    clearTimeout(deadline)
    clearTimeout(pongDeadline)
    clearInterval(heartbeat)
    socket.close()
    failPending(refusal ?? reason)
    onEnd(refusal ?? reason, final || refusal !== undefined)
  }
  let queue: Promise<void> = Promise.resolve()
  const inOrder = (task: () => Promise<void> | void) => {
    queue = queue.then(async () => {
      if (ended) return
      try {
        await task()
      } catch (error) {
        end(`talking to ${home.homeName} failed: ${error instanceof Error ? error.message : String(error)}`)
      }
    })
  }

  const send = (type: number, payload: Uint8Array) => socket.send(encodeFrame(type, connId, payload))
  const sendHomeGateway = async (message: object) => {
    const sending = transport
    if (!sending) return
    const sealed = await sending.send.encrypt(new TextEncoder().encode(JSON.stringify(message)))
    if (!ended) send(FrameType.data, sealed)
  }

  const authPayload = (nonce: Uint8Array): Uint8Array => {
    const signature = signChallenge(keys, home.homeId, nonce)
    return pairing ? concatBytes(signature, relayToken(pairing.secret)) : signature
  }

  const handshake = async () => {
    const first = pairing
      ? { pairing_id: pairing.pairingId, device_name: keys.name }
      : { profile_id: home.profileId }
    const started = await startHandshake(
      {
        kind: pairing ? HandshakeKind.pair : HandshakeKind.paired,
        staticPrivate: keys.noiseSecret,
        homeStaticPublic: fromBase64Url(home.homeNoisePub),
        phoneSignPub: keys.signPub,
        ...(pairing ? { psk: pairing.secret } : {}),
      },
      new TextEncoder().encode(JSON.stringify(first)),
    )
    if (ended) return
    transport = undefined
    homeAnswersPing = false
    welcomed = false
    asks.clear()
    seeds.clear()
    answered()
    finishHandshake = started.finish
    send(FrameType.data, started.firstFrame)
  }

  const receive = (plaintext: Uint8Array) => {
    if (plaintext[0] === outputFrame) return onTerminal(decodeOutputFrame(plaintext))
    const event = decodeHomeGatewayEvent(new TextDecoder().decode(plaintext))
    switch (event?.event) {
      case 'welcome':
        clearTimeout(deadline)
        welcomed = true
        homeAnswersPing = event.api >= homePingApi
        return onEvent({
          _tag: 'Welcomed',
          api: event.api,
          homeName: event.home_name,
          profileName: event.profile.name,
        })
      case 'terminal_screen':
        return onTerminal({ _tag: 'Screen', screen: event, ansi: fromBase64(event.ansi) })
      case 'terminal_resized':
        return onTerminal({
          _tag: 'Resized',
          terminalId: event.terminal_id,
          cols: event.cols,
          rows: event.rows,
        })
      case 'terminal_ended':
        return onTerminal({ _tag: 'Ended', terminalId: event.terminal_id })
      case 'terminal_error':
        return onTerminal({ _tag: 'Failed', terminalId: event.terminal_id, message: event.message })
      case 'sessions':
        return onEvent({
          _tag: 'Sessions',
          sessions: event.sessions,
          crewBetweenSessions: event.crew_between_sessions ?? [],
        })
      case 'refused':
        refusal = `${home.homeName} refused this phone: ${event.message}`
        return
      case 'pong':
        return answered()
      case 'ask':
        asks.set(event.ask.id, event.ask)
        return
      case 'asks':
        for (const id of asks.keys()) if (!event.ids.includes(id)) asks.delete(id)
        return onEvent({ _tag: 'Asks', asks: event.ids.flatMap((id) => asks.get(id) ?? []) })
      case 'garden_seeds':
        for (const seed of event.seeds) seeds.set(seed.id, seed)
        return
      case 'garden': {
        const open = new Set(event.ids)
        for (const id of seeds.keys()) if (!open.has(id)) seeds.delete(id)
        return onEvent({ _tag: 'Garden', seeds: event.ids.flatMap((id) => seeds.get(id) ?? []) })
      }
      case 'seed_changed':
        return onEvent({ _tag: 'SeedChanged', seedId: event.seed_id })
      case 'seed_detail':
      case 'seed_error':
      case 'seed_result':
      case 'submit_result':
      case 'ask_answer_result':
      case 'ask_seed_mentions_result':
      case 'ask_image_chunk':
      case 'ask_image_error':
      case 'crew_start_result': {
        const request = pending.get(event.request_id)
        pending.delete(event.request_id)
        return request?.resolve(event)
      }
      case undefined:
        return
      default:
        return event satisfies never
    }
  }

  const onData = async (payload: Uint8Array) => {
    if (finishHandshake) {
      const finish = finishHandshake
      finishHandshake = undefined
      const finished = await finish(payload)
      if (ended) return
      transport = finished
      return sendHomeGateway({ cmd: 'hello', api: HOME_GATEWAY_API_VERSION, device_id: keys.id })
    }
    if (!transport) return
    const plaintext = await transport.recv.decrypt(payload)
    if (!ended) receive(plaintext)
  }

  socket.onmessage = (message: MessageEvent<ArrayBuffer | string>) => {
    if (typeof message.data === 'string') {
      if (message.data === 'pong') answered()
      return
    }
    const frame = decodeFrame(new Uint8Array(message.data))
    inOrder(() => {
      switch (frame.type) {
        case FrameType.challenge:
          return send(FrameType.auth, authPayload(frame.payload))
        case FrameType.ready:
          connId = frame.connId
          return
        case FrameType.presence:
          if (frame.payload[0] === 1) {
            armDeadline()
            return handshake()
          }
          clearTimeout(deadline)
          answered()
          finishHandshake = undefined
          transport = undefined
          welcomed = false
          failPending(`${home.homeName} went offline`)
          return onEvent({ _tag: 'HomeOffline' })
        case FrameType.data:
          return onData(frame.payload)
      }
    })
  }
  socket.onclose = (event) =>
    end(
      closeReason(home, url, event, connId !== 0, pairing !== undefined),
      event.code === 4000 || event.code === 4403,
    )
  armDeadline()

  return {
    close: () => {
      ended = true
      clearTimeout(deadline)
      clearTimeout(pongDeadline)
      clearInterval(heartbeat)
      socket.close()
      failPending(`the connection to ${home.homeName} closed`)
    },
    request: (command) =>
      new Promise((resolve, reject) => {
        if (ended || !transport || !welcomed) return reject(new Error(`${home.homeName} is not connected`))
        const requestId = crypto.randomUUID()
        pending.set(requestId, { resolve, reject })
        inOrder(() => sendHomeGateway({ ...command, request_id: requestId }))
      }),
    send: (message) => {
      if (ended || !transport || !welcomed) return false
      inOrder(() => sendHomeGateway(message))
      return true
    },
  }
}

type Attached = { request: TerminalRequest; readonly sink: TerminalSink }

const olderHome = (homeName: string) =>
  `attn on ${homeName} is too old to open terminals from a phone; update it to the latest attn`

export const runLink = (home: Home, keys: PhoneKeys, onEvent: (event: LinkEvent) => void): Link => {
  let retryMs = firstRetryMs
  let retry: ReturnType<typeof setTimeout> | undefined
  let socket: Socket | undefined
  let api = 0
  const terminals = new Map<string, Attached>()

  const attach = (terminalId: string, attached: Attached) =>
    api >= homeTerminalApi &&
    socket?.send({
      cmd: 'terminal_attach',
      terminal_id: terminalId,
      cols: attached.request.cols,
      rows: attached.request.rows,
      credit: terminalCreditBytes,
    }) &&
    socket.send({ cmd: 'terminal_focus', terminal_id: terminalId })

  const onTerminal = (event: TerminalEvent) => {
    const terminalId = event._tag === 'Screen' ? event.screen.terminal_id : event.terminalId
    const sink = terminals.get(terminalId)?.sink
    if (!sink) return
    switch (event._tag) {
      case 'Output':
        return sink.output(event.data)
      case 'Screen':
        return sink.screen(event.screen, event.ansi)
      case 'Resized':
        return sink.resized(event.cols, event.rows)
      case 'Ended':
        return sink.ended()
      case 'Failed':
        return sink.failed(event.message)
    }
  }

  const attempt = () => {
    socket = openSocket(
      home,
      keys,
      undefined,
      (event) => {
        if (event._tag !== 'Welcomed') return onEvent(event)
        retryMs = firstRetryMs
        api = event.api
        onEvent(event)
        for (const [terminalId, attached] of terminals) {
          if (api < homeTerminalApi) attached.sink.failed(olderHome(home.homeName))
          else attach(terminalId, attached)
        }
      },
      onTerminal,
      (reason, final) => {
        api = 0
        if (final) return onEvent({ _tag: 'Closed', reason })
        onEvent({ _tag: 'Lost', reason })
        retry = setTimeout(attempt, retryMs)
        retryMs = Math.min(retryMs * 2, maxRetryMs)
      },
    )
  }
  attempt()
  return {
    close: () => {
      clearTimeout(retry)
      terminals.clear()
      socket?.close()
    },
    request: (command) => socket!.request(command),
    openTerminal: (terminalId, request, sink) => {
      const attached: Attached = { request, sink }
      terminals.set(terminalId, attached)
      if (api > 0 && api < homeTerminalApi) sink.failed(olderHome(home.homeName))
      else attach(terminalId, attached)
      const current = () => terminals.get(terminalId) === attached
      return {
        send: (message) => current() && (socket?.send(message) ?? false),
        grant: (bytes) => {
          if (current()) socket?.send({ cmd: 'terminal_credit', terminal_id: terminalId, bytes })
        },
        resize: (next) => {
          if (!current()) return
          attached.request = next
          socket?.send({ cmd: 'terminal_resize', terminal_id: terminalId, cols: next.cols, rows: next.rows })
        },
        close: () => {
          if (!current()) return
          terminals.delete(terminalId)
          socket?.send({ cmd: 'terminal_detach', terminal_id: terminalId })
        },
      }
    },
  }
}

export const pairWithHome = (
  home: Home,
  keys: PhoneKeys,
  pairing: Pairing,
): Promise<{ homeName: string; profileName: string }> =>
  new Promise((resolve, reject) => {
    const { close } = openSocket(
      home,
      keys,
      pairing,
      (event) => {
        if (event._tag === 'Welcomed') {
          close()
          resolve(event)
        } else if (event._tag === 'HomeOffline') {
          close()
          reject(new Error(`${home.homeName} is offline; open attn on it and scan again`))
        }
      },
      () => {},
      (reason) => reject(new Error(reason)),
    )
  })
