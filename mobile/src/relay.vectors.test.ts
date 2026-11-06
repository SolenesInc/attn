import { CapacitorHttp } from '@capacitor/core'
import { readFileSync } from 'node:fs'
import { ed25519, x25519 } from '@noble/curves/ed25519.js'
import { sha256 } from '@noble/hashes/sha2.js'
import { bytesToHex, hexToBytes } from '@noble/hashes/utils.js'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toBase64Url } from './bytes'
import { relayToken, type DeviceKeys } from './device'
import { pairWithHome, runLink, type LinkEvent } from './link'
import { pushTokenMessage, registerForPush } from './push'
import { FakeRelay, FakeSocket, frame } from './test/fakeRelay'

vi.mock('@capacitor/core', async (actual) => ({
  ...(await actual<typeof import('@capacitor/core')>()),
  CapacitorHttp: { post: vi.fn() },
}))

type Signed = { message: string; signature: string }
type Vectors = {
  sign_seed: string
  sign_pub: string
  home_id: string
  nonce: string
  push_token: string
  relay_token: { qr_secret: string; token: string; token_hash: string }
  frames: ReadonlyArray<{ name: string; type: number; conn_id: number; payload: string; bytes: string }>
  signatures: { auth_device: Signed; push_token: Signed }
}

const vectors: Vectors = JSON.parse(
  readFileSync(new URL('../../internal/cloudlink/testdata/relay_vectors.json', import.meta.url), 'utf8'),
)
const named = (name: string) => vectors.frames.find((each) => each.name === name)!
const wire = (name: string) => hexToBytes(named(name).bytes).buffer as ArrayBuffer

const signSecret = hexToBytes(vectors.sign_seed)
const keys: DeviceKeys = {
  signSecret,
  signPub: ed25519.getPublicKey(signSecret),
  noiseSecret: x25519.utils.randomSecretKey(),
  id: 'device',
  name: 'Test iPhone',
}

class ScriptedSocket {
  static latest: ScriptedSocket
  binaryType = 'arraybuffer'
  onmessage?: (event: { data: ArrayBuffer | string }) => void
  onclose?: (event: { code: number }) => void
  readonly sent: Array<Uint8Array> = []
  constructor(readonly url: string) {
    ScriptedSocket.latest = this
  }
  send(data: Uint8Array | string) {
    if (typeof data !== 'string') this.sent.push(data)
  }
  close() {}
}

describe('the phone speaks the relay contract vectors', () => {
  const events: Array<LinkEvent> = []
  let closeLink = () => {}

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    vi.stubGlobal('WebSocket', ScriptedSocket)
    events.length = 0
    closeLink = runLink(
      {
        cloud: 'https://cloud.example',
        homeId: vectors.home_id,
        homeNoisePub: toBase64Url(x25519.getPublicKey(x25519.utils.randomSecretKey())),
        homeName: 'Mac',
        profileId: 'p',
      },
      keys,
      (event) => events.push(event),
    ).close
  })

  afterEach(() => {
    closeLink()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  const deliver = async (name: string) => {
    ScriptedSocket.latest.onmessage?.({ data: wire(name) })
    await vi.advanceTimersByTimeAsync(1)
  }

  it('dials the device route with its key', () => {
    expect(ScriptedSocket.latest.url).toBe(
      `wss://cloud.example/v1/homes/${vectors.home_id}/device?device_sign_pub=${vectors.sign_pub}`,
    )
  })

  it('answers the challenge with the signature the contract specifies', async () => {
    await deliver('challenge')
    expect(ScriptedSocket.latest.sent.map(bytesToHex)).toEqual([
      `010200000000${vectors.signatures.auth_device.signature}`,
    ])
  })

  it('takes its conn_id from ready, starts Noise on presence online, and sees presence offline', async () => {
    await deliver('ready_device')
    await deliver('presence_online')
    const [first] = ScriptedSocket.latest.sent
    expect(bytesToHex(first!.subarray(0, 6))).toBe('010500000007')

    await deliver('presence_offline')
    expect(events.map((event) => event._tag)).toContain('HomeOffline')
  })

  it('signs a push token over the bytes the contract specifies', () => {
    const { message, signature } = vectors.signatures.push_token
    const signed = pushTokenMessage(vectors.home_id, vectors.sign_pub, 'sandbox', vectors.push_token)
    expect(bytesToHex(signed)).toBe(message)
    expect(bytesToHex(ed25519.sign(signed, signSecret))).toBe(signature)
  })
})

describe('the phone pairs with the relay token the contract specifies', () => {
  const home = {
    cloud: 'https://cloud.example',
    homeId: vectors.home_id,
    homeNoisePub: toBase64Url(x25519.getPublicKey(x25519.utils.randomSecretKey())),
    homeName: 'Mac',
    profileId: 'p',
  }
  const pairing = { pairingId: 'p1', secret: hexToBytes(vectors.relay_token.qr_secret) }

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    vi.stubGlobal('WebSocket', ScriptedSocket)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('derives the token and its hash from the QR secret', () => {
    const token = relayToken(pairing.secret)
    expect(bytesToHex(token)).toBe(vectors.relay_token.token)
    expect(bytesToHex(sha256(token))).toBe(vectors.relay_token.token_hash)
  })

  it('sends the token after the signature only while pairing', async () => {
    const paired = pairWithHome(home, keys, pairing)
    paired.catch(() => {})
    ScriptedSocket.latest.onmessage?.({ data: wire('challenge') })
    await vi.advanceTimersByTimeAsync(1)
    expect(ScriptedSocket.latest.sent.map(bytesToHex)).toEqual([
      `010200000000${vectors.signatures.auth_device.signature}${vectors.relay_token.token}`,
    ])
  })

  it('tells the person to show a new code when the relay refuses it', async () => {
    const paired = pairWithHome(home, keys, pairing)
    ScriptedSocket.latest.onclose?.({ code: 4403 })
    await expect(paired).rejects.toThrow(
      'this pairing code no longer works; show a new one in Settings → Phones on Mac',
    )
  })
})

describe('the phone registers its push token as the contract specifies', () => {
  it('posts the signed body to the push-token route', async () => {
    const stored = JSON.stringify({ sign: toBase64Url(signSecret), noise: toBase64Url(keys.noiseSecret) })
    vi.stubGlobal('localStorage', { getItem: () => stored, setItem: () => {} })
    const post = vi.mocked(CapacitorHttp.post)
    post.mockResolvedValue({ status: 200, data: {}, headers: {}, url: '' })

    await registerForPush(
      { cloud: 'https://cloud.example', homeId: vectors.home_id },
      { token: vectors.push_token, environment: 'sandbox' },
    )

    expect(post).toHaveBeenCalledWith({
      url: `https://cloud.example/v1/homes/${vectors.home_id}/push-token`,
      headers: { 'content-type': 'application/json' },
      data: {
        device_sign_pub: vectors.sign_pub,
        platform: 'apns',
        environment: 'sandbox',
        token: vectors.push_token,
        sig: toBase64Url(hexToBytes(vectors.signatures.push_token.signature)),
      },
    })
    vi.unstubAllGlobals()
  })
})

describe('the phone tests stand-in relay speaks the relay contract', () => {
  it.each(vectors.frames.map((each) => [each.name, each] as const))('%s', (_, vector) => {
    const bytes = new Uint8Array(frame(vector.type, vector.conn_id, hexToBytes(vector.payload)))
    expect(bytesToHex(bytes)).toBe(vector.bytes)
  })

  it('opens a device socket with challenge, then ready and presence online', async () => {
    const seen: Array<number> = []
    const socket = new FakeSocket(`wss://x/?device_sign_pub=${vectors.sign_pub}`, new FakeRelay())
    socket.onmessage = ({ data }) => seen.push(new Uint8Array(data as ArrayBuffer)[1]!)
    await Promise.resolve()
    socket.send(hexToBytes(named('auth').bytes))
    await Promise.resolve()
    await Promise.resolve()
    expect(seen).toEqual([named('challenge').type, named('ready_device').type, named('presence_online').type])
  })
})
