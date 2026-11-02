import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toBase64Url } from './bytes'
import type { DeviceKeys } from './device'
import { pingIntervalMs, pongDeadlineMs, runLink, type LinkEvent } from './link'
import { FakeRelay, socketsOf } from './test/fakeRelay'

let relay: FakeRelay
let events: Array<LinkEvent>
let close: () => void

const signSecret = ed25519.utils.randomSecretKey()
const keys: DeviceKeys = {
  signSecret,
  signPub: ed25519.getPublicKey(signSecret),
  noiseSecret: x25519.utils.randomSecretKey(),
  id: 'device',
  name: 'Test iPhone',
}

const connect = async () => {
  close = runLink(
    {
      cloud: 'https://cloud.example',
      homeId: 'home',
      homeNoisePub: toBase64Url(relay.homeNoisePub),
      homeName: 'Mac',
      profileId: 'p',
    },
    keys,
    (event) => events.push(event),
  ).close
  await vi.advanceTimersByTimeAsync(1)
}

const tags = () => events.map((event) => event._tag)

beforeEach(() => {
  vi.useFakeTimers()
  relay = new FakeRelay()
  events = []
  vi.stubGlobal('WebSocket', socketsOf(relay))
})

afterEach(() => {
  close()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('a phone keeps checking that its home still answers', () => {
  it('stays live for as long as the home answers its pings', async () => {
    await connect()
    expect(tags()).toEqual(['Welcomed', 'Sessions'])

    await vi.advanceTimersByTimeAsync(10 * pingIntervalMs)

    expect(relay.homePings).toBe(10)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
    expect(relay.sockets).toHaveLength(1)
  })

  it('reconnects when a home stops answering although its relay still does', async () => {
    await connect()
    relay.homeAnswersPings = false

    await vi.advanceTimersByTimeAsync(pingIntervalMs + pongDeadlineMs - 1000)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])

    await vi.advanceTimersByTimeAsync(1000)
    expect(events.at(-1)).toEqual({ _tag: 'Lost', reason: 'Mac: no answer for 10 s; reconnecting' })
    expect(relay.sockets[0]!.closed).toBe(true)

    relay.homeAnswersPings = true
    await vi.advanceTimersByTimeAsync(100)
    expect(relay.sockets).toHaveLength(2)
    expect(tags().slice(-2)).toEqual(['Welcomed', 'Sessions'])
  })

  it('pings only the relay when the home serves api 1, which has no ping', async () => {
    relay.welcomeApi = 1
    relay.homeAnswersPings = false
    await connect()

    await vi.advanceTimersByTimeAsync(10 * pingIntervalMs)

    expect(relay.homePings).toBe(0)
    expect(tags()).toEqual(['Welcomed', 'Sessions'])
  })
})
