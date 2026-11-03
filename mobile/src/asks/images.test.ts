import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toBase64Url } from '../bytes'
import type { DeviceKeys } from '../device'
import { runLink, type Link } from '../link'
import { FakeRelay, socketsOf } from '../test/fakeRelay'
import { ImageRefused, chunkWindow, fetchAskImage } from './images'

let relay: FakeRelay
let link: Link

const signSecret = ed25519.utils.randomSecretKey()
const keys: DeviceKeys = {
  signSecret,
  signPub: ed25519.getPublicKey(signSecret),
  noiseSecret: x25519.utils.randomSecretKey(),
  id: 'device',
  name: 'Test iPhone',
}

const connect = async () => {
  link = runLink(
    {
      cloud: 'https://cloud.example',
      homeId: 'home',
      homeNoisePub: toBase64Url(relay.homeNoisePub),
      homeName: 'Mac',
      profileId: 'p',
    },
    keys,
    () => {},
  )
  await vi.advanceTimersByTimeAsync(1)
}

const board = Uint8Array.from({ length: 300_000 }, (_, i) => (i * 7) % 256)

beforeEach(() => {
  vi.useFakeTimers()
  relay = new FakeRelay()
  relay.images.set('a-1\nshots/board.png', { mimeType: 'image/png', bytes: board })
  vi.stubGlobal('WebSocket', socketsOf(relay))
})

afterEach(() => {
  link.close()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('a phone reads an ask image from its home in chunks', () => {
  it('assembles the image while keeping at most the window of chunk requests unanswered', async () => {
    await connect()
    relay.holdImageReplies = true
    const fetched = fetchAskImage(link, 'a-1', 'shots/board.png')
    await vi.advanceTimersByTimeAsync(1)
    expect(relay.heldImageRequests).toHaveLength(1)

    let mostUnanswered = 0
    while (relay.heldImageRequests.length > 0) {
      mostUnanswered = Math.max(mostUnanswered, relay.heldImageRequests.length)
      relay.heldImageRequests.shift()!()
      await vi.advanceTimersByTimeAsync(1)
    }

    const image = await fetched
    expect(mostUnanswered).toBe(chunkWindow)
    expect(image.type).toBe('image/png')
    expect(new Uint8Array(await image.arrayBuffer())).toEqual(board)
  })

  it('fails with the reason the home gave when it cannot read the image', async () => {
    await connect()
    const failed = fetchAskImage(link, 'a-1', 'gone.png')
    await expect(failed).rejects.toThrow(new ImageRefused('no copy of gone.png'))
  })

  it('fails the load, as one to try again, when the connection drops mid-image', async () => {
    await connect()
    relay.holdImageReplies = true
    const fetched = fetchAskImage(link, 'a-1', 'shots/board.png')
    const outcome = fetched.then(
      () => 'loaded',
      (error: Error) => error,
    )
    await vi.advanceTimersByTimeAsync(1)
    relay.sockets[0]!.close()
    await vi.advanceTimersByTimeAsync(1)

    const error = await outcome
    expect(error).toBeInstanceOf(Error)
    expect(error).not.toBeInstanceOf(ImageRefused)
    expect((error as Error).message).toContain('Mac')
  })

  it('fails at once, instead of waiting forever, when asked while the link waits to reconnect', async () => {
    await connect()
    relay.sockets[0]!.close()
    await vi.advanceTimersByTimeAsync(1)

    await expect(fetchAskImage(link, 'a-1', 'shots/board.png')).rejects.toThrow('Mac is not connected')
  })
})
