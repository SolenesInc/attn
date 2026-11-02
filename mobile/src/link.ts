import { decodeGatewayEvent } from '@attn/gateway/decode'
import type { GatewaySession } from '@attn/types/gatewayGenerated'
import { GATEWAY_API_VERSION } from '@attn/types/gatewayVersion'
import { fromBase64Url, toBase64Url } from './bytes'
import { signChallenge, type DeviceKeys } from './device'
import { HandshakeKind, startHandshake, type Transport } from './noise'

export const connectTimeoutMs = 10_000
export const pingIntervalMs = 15_000
export const pongDeadlineMs = 10_000
const homePingApi = 2
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
  | { readonly _tag: 'Welcomed'; readonly homeName: string; readonly profileName: string }
  | { readonly _tag: 'Sessions'; readonly sessions: ReadonlyArray<GatewaySession> }
  | { readonly _tag: 'HomeOffline' }
  | { readonly _tag: 'Lost'; readonly reason: string }
  | { readonly _tag: 'Closed'; readonly reason: string }

type Pairing = { readonly pairingId: string; readonly secret: Uint8Array }

const deviceAddress = (home: Home): string =>
  `${home.cloud.replace(/^http/, 'ws')}/v1/homes/${home.homeId}/device`

const closeReason = (home: Home, url: string, event: CloseEvent, ready: boolean): string => {
  switch (event.code) {
    case 4000:
      return `${home.homeName} ended this pairing; to use it again, pair from Settings → Phones`
    case 4403:
      return `${home.homeName} no longer allows this phone; pair it again from Settings → Phones`
    case 4400:
      return `the relay refused a malformed frame: ${event.reason}`
    case 4401:
      return 'the relay refused this phone’s signature'
    default:
      return ready
        ? `the connection to ${home.homeName} dropped (close code ${event.code})`
        : `could not reach ${home.homeName} at ${url} (close code ${event.code})`
  }
}

const openSocket = (
  home: Home,
  keys: DeviceKeys,
  pairing: Pairing | undefined,
  onEvent: (event: LinkEvent) => void,
  onEnd: (reason: string, final: boolean) => void,
): (() => void) => {
  const url = deviceAddress(home)
  const socket = new WebSocket(`${url}?device_sign_pub=${toBase64Url(keys.signPub)}`)
  socket.binaryType = 'arraybuffer'
  let ended = false
  let connId = 0
  let finishHandshake: ((reply: Uint8Array) => Transport) | undefined
  let transport: Transport | undefined
  let refusal: string | undefined
  let homeAnswersPing = false
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
    const pinged = transport && homeAnswersPing ? home.homeName : `the relay for ${home.homeName}`
    pongDeadline = setTimeout(
      () => end(`${pinged}: no answer for ${pongDeadlineMs / 1000} s; reconnecting`),
      pongDeadlineMs,
    )
    if (transport && homeAnswersPing) sendGateway({ cmd: 'ping' })
    else socket.send('ping')
  }, pingIntervalMs)
  const answered = () => {
    clearTimeout(pongDeadline)
    pongDeadline = undefined
  }

  const end = (reason: string, final = false) => {
    if (ended) return
    ended = true
    clearTimeout(deadline)
    clearTimeout(pongDeadline)
    clearInterval(heartbeat)
    socket.close()
    onEnd(refusal ?? reason, final || refusal !== undefined)
  }
  const send = (type: number, payload: Uint8Array) => socket.send(encodeFrame(type, connId, payload))
  const sendGateway = (message: object) =>
    send(FrameType.data, transport!.send.encrypt(new TextEncoder().encode(JSON.stringify(message))))

  const handshake = () => {
    const first = pairing
      ? { pairing_id: pairing.pairingId, device_name: keys.name }
      : { profile_id: home.profileId }
    const started = startHandshake(
      {
        kind: pairing ? HandshakeKind.pair : HandshakeKind.paired,
        staticPrivate: keys.noiseSecret,
        homeStaticPublic: fromBase64Url(home.homeNoisePub),
        deviceSignPub: keys.signPub,
        ...(pairing ? { psk: pairing.secret } : {}),
      },
      new TextEncoder().encode(JSON.stringify(first)),
    )
    transport = undefined
    homeAnswersPing = false
    answered()
    finishHandshake = started.finish
    send(FrameType.data, started.firstFrame)
  }

  const receive = (plaintext: Uint8Array) => {
    const event = decodeGatewayEvent(new TextDecoder().decode(plaintext))
    switch (event?.event) {
      case 'welcome':
        clearTimeout(deadline)
        homeAnswersPing = event.api >= homePingApi
        return onEvent({ _tag: 'Welcomed', homeName: event.home_name, profileName: event.profile.name })
      case 'sessions':
        return onEvent({ _tag: 'Sessions', sessions: event.sessions })
      case 'refused':
        refusal = `${home.homeName} refused this phone: ${event.message}`
        return
      case 'pong':
        return answered()
    }
  }

  const onData = (payload: Uint8Array) => {
    if (finishHandshake) {
      transport = finishHandshake(payload)
      finishHandshake = undefined
      return sendGateway({ cmd: 'hello', api: GATEWAY_API_VERSION, device_id: keys.id })
    }
    if (transport) receive(transport.recv.decrypt(payload))
  }

  socket.onmessage = (message: MessageEvent<ArrayBuffer | string>) => {
    if (typeof message.data === 'string') {
      if (message.data === 'pong') answered()
      return
    }
    const frame = decodeFrame(new Uint8Array(message.data))
    try {
      switch (frame.type) {
        case FrameType.challenge:
          return send(FrameType.auth, signChallenge(keys, home.homeId, frame.payload))
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
          return onEvent({ _tag: 'HomeOffline' })
        case FrameType.data:
          return onData(frame.payload)
      }
    } catch (error) {
      end(`talking to ${home.homeName} failed: ${error instanceof Error ? error.message : String(error)}`)
    }
  }
  socket.onclose = (event) =>
    end(closeReason(home, url, event, connId !== 0), event.code === 4000 || event.code === 4403)
  armDeadline()

  return () => {
    ended = true
    clearTimeout(deadline)
    clearTimeout(pongDeadline)
    clearInterval(heartbeat)
    socket.close()
  }
}

export const runLink = (home: Home, keys: DeviceKeys, onEvent: (event: LinkEvent) => void): (() => void) => {
  let retryMs = firstRetryMs
  let retry: ReturnType<typeof setTimeout> | undefined
  let close = () => {}
  const attempt = () => {
    close = openSocket(
      home,
      keys,
      undefined,
      (event) => {
        if (event._tag === 'Welcomed') retryMs = firstRetryMs
        onEvent(event)
      },
      (reason, final) => {
        if (final) return onEvent({ _tag: 'Closed', reason })
        onEvent({ _tag: 'Lost', reason })
        retry = setTimeout(attempt, retryMs)
        retryMs = Math.min(retryMs * 2, maxRetryMs)
      },
    )
  }
  attempt()
  return () => {
    clearTimeout(retry)
    close()
  }
}

export const pairWithHome = (
  home: Home,
  keys: DeviceKeys,
  pairing: Pairing,
): Promise<{ homeName: string; profileName: string }> =>
  new Promise((resolve, reject) => {
    const close = openSocket(
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
      (reason) => reject(new Error(reason)),
    )
  })
