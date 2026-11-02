import { chacha20poly1305 } from '@noble/ciphers/chacha.js'
import { x25519 } from '@noble/curves/ed25519.js'
import { hmac } from '@noble/hashes/hmac.js'
import { sha256 } from '@noble/hashes/sha2.js'

const text = new TextEncoder()
const join = (...parts: ReadonlyArray<Uint8Array>) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let at = 0
  for (const part of parts) {
    out.set(part, at)
    at += part.length
  }
  return out
}

class Cipher {
  private n = 0n
  constructor(private readonly key: Uint8Array) {}
  private nonce() {
    const nonce = new Uint8Array(12)
    new DataView(nonce.buffer).setBigUint64(4, this.n++, true)
    return nonce
  }
  seal(plaintext: Uint8Array, ad = new Uint8Array(0)) {
    return chacha20poly1305(this.key, this.nonce(), ad).encrypt(plaintext)
  }
  open(ciphertext: Uint8Array, ad = new Uint8Array(0)) {
    return chacha20poly1305(this.key, this.nonce(), ad).decrypt(ciphertext)
  }
}

const kdf = (ck: Uint8Array, ikm: Uint8Array) => {
  const temp = hmac(sha256, ck, ikm)
  const one = hmac(sha256, temp, Uint8Array.of(1))
  return [one, hmac(sha256, temp, join(one, Uint8Array.of(2)))] as const
}

const homeAnswersPairedHandshake = (homeStatic: Uint8Array, prologue: Uint8Array, msg1: Uint8Array) => {
  const name = text.encode('Noise_IK_25519_ChaChaPoly_SHA256')
  let h = join(name, new Uint8Array(32 - name.length))
  let ck = h
  let k: Cipher | undefined
  const mixHash = (data: Uint8Array) => (h = sha256(join(h, data)))
  const mixKey = (ikm: Uint8Array) => {
    const [nextCk, key] = kdf(ck, ikm)
    ck = nextCk
    k = new Cipher(key)
  }
  const decryptAndHash = (data: Uint8Array) => {
    const out = k!.open(data, h)
    mixHash(data)
    return out
  }
  const encryptAndHash = (data: Uint8Array) => {
    const out = k!.seal(data, h)
    mixHash(out)
    return out
  }
  mixHash(prologue)
  mixHash(x25519.getPublicKey(homeStatic))
  const re = msg1.subarray(0, 32)
  mixHash(re)
  mixKey(x25519.getSharedSecret(homeStatic, re))
  const rs = decryptAndHash(msg1.subarray(32, 80))
  mixKey(x25519.getSharedSecret(homeStatic, rs))
  decryptAndHash(msg1.subarray(80))
  const e = x25519.utils.randomSecretKey()
  const ePub = x25519.getPublicKey(e)
  mixHash(ePub)
  mixKey(x25519.getSharedSecret(e, re))
  mixKey(x25519.getSharedSecret(e, rs))
  const msg2 = join(ePub, encryptAndHash(new Uint8Array(0)))
  const [toHome, toDevice] = kdf(ck, new Uint8Array(0))
  return { msg2, recv: new Cipher(toHome), send: new Cipher(toDevice) }
}

const frame = (type: number, connId: number, payload: Uint8Array = new Uint8Array(0)) => {
  const out = new Uint8Array(6 + payload.length)
  out[0] = 1
  out[1] = type
  new DataView(out.buffer).setUint32(2, connId)
  out.set(payload, 6)
  return out.buffer
}

const imageChunkBytes = 40 << 10

type Listener = {
  onmessage?: (event: { data: ArrayBuffer | string }) => void
  onclose?: (event: { code: number }) => void
}

export class FakeRelay {
  readonly homeStatic = x25519.utils.randomSecretKey()
  readonly sockets: Array<FakeSocket> = []
  homeAnswersPings = true
  homePings = 0
  welcomeApi: number | undefined
  readonly images = new Map<string, { mimeType: string; bytes: Uint8Array }>()
  readonly heldImageRequests: Array<() => void> = []
  holdImageReplies = false

  get homeNoisePub() {
    return x25519.getPublicKey(this.homeStatic)
  }

  sessions(socket: FakeSocket, sessions: ReadonlyArray<object>) {
    socket.toPhone({ event: 'sessions', sessions })
  }
}

export class FakeSocket implements Listener {
  binaryType = 'arraybuffer'
  onmessage?: Listener['onmessage']
  onclose?: Listener['onclose']
  closed = false
  private readonly connId = 7
  private readonly signPub: Uint8Array
  private transport: ReturnType<typeof homeAnswersPairedHandshake> | undefined

  constructor(
    readonly url: string,
    private readonly relay: FakeRelay,
  ) {
    const key = new URL(url).searchParams.get('device_sign_pub')!
    this.signPub = Uint8Array.from(atob(key.replaceAll('-', '+').replaceAll('_', '/')), (c) =>
      c.charCodeAt(0),
    )
    this.deliver(frame(1, 0, new Uint8Array(32)))
  }

  private deliver(data: ArrayBuffer | string) {
    queueMicrotask(() => !this.closed && this.onmessage?.({ data }))
  }

  toPhone(message: object) {
    this.deliver(frame(5, this.connId, this.transport!.send.seal(text.encode(JSON.stringify(message)))))
  }

  send(data: Uint8Array | string) {
    if (typeof data === 'string') {
      if (data === 'ping') this.deliver('pong')
      return
    }
    const type = data[1]
    const payload = data.subarray(6)
    if (type === 2) {
      this.deliver(frame(3, this.connId))
      this.deliver(frame(8, this.connId, Uint8Array.of(1)))
      return
    }
    if (!this.transport) {
      this.transport = homeAnswersPairedHandshake(
        this.relay.homeStatic,
        join(text.encode('attn-cloud/v1 noise\n'), this.signPub),
        payload.subarray(1),
      )
      this.deliver(frame(5, this.connId, this.transport.msg2))
      return
    }
    const message = JSON.parse(new TextDecoder().decode(this.transport.recv.open(payload)))
    if (message.cmd === 'hello') {
      this.toPhone({
        event: 'welcome',
        api: this.relay.welcomeApi ?? message.api,
        home_name: 'Mac',
        profile: { id: 'p', name: 'personal' },
      })
      this.relay.sessions(this, [])
    }
    if (message.cmd === 'ask_image') {
      const reply = () => this.answerImage(message)
      if (this.relay.holdImageReplies) this.relay.heldImageRequests.push(reply)
      else reply()
    }
    if (message.cmd === 'ping') {
      this.relay.homePings++
      if (this.relay.homeAnswersPings) this.toPhone({ event: 'pong' })
    }
  }

  private answerImage(message: { request_id: string; ask_id: string; src: string; offset: number }) {
    const image = this.relay.images.get(`${message.ask_id}\n${message.src}`)
    if (!image) {
      this.toPhone({
        event: 'ask_image_error',
        request_id: message.request_id,
        message: `no copy of ${message.src}`,
      })
      return
    }
    const chunk = image.bytes.subarray(message.offset, message.offset + imageChunkBytes)
    this.toPhone({
      event: 'ask_image_chunk',
      request_id: message.request_id,
      offset: message.offset,
      size: image.bytes.length,
      mime_type: image.mimeType,
      data_base64: btoa(String.fromCharCode(...chunk)),
    })
  }

  close() {
    if (this.closed) return
    this.closed = true
    queueMicrotask(() => this.onclose?.({ code: 1000 }))
  }
}

export const socketsOf = (relay: FakeRelay) =>
  class extends FakeSocket {
    constructor(url: string) {
      super(url, relay)
      relay.sockets.push(this)
    }
  }
