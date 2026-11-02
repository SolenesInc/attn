import { chacha20poly1305 } from '@noble/ciphers/chacha.js'
import { x25519 } from '@noble/curves/ed25519.js'
import { hmac } from '@noble/hashes/hmac.js'
import { sha256 } from '@noble/hashes/sha2.js'

export const HandshakeKind = { paired: 1, pair: 2 } as const
export type HandshakeKind = (typeof HandshakeKind)[keyof typeof HandshakeKind]

const encoder = new TextEncoder()
const empty = new Uint8Array(0)

export const prologue = (deviceSignPub: Uint8Array): Uint8Array =>
  concat(encoder.encode('attn-cloud/v1 noise\n'), deviceSignPub)

export const concat = (...parts: ReadonlyArray<Uint8Array>): Uint8Array => {
  const out = new Uint8Array(parts.reduce((size, part) => size + part.length, 0))
  let offset = 0
  for (const part of parts) {
    out.set(part, offset)
    offset += part.length
  }
  return out
}

const nonceBytes = (n: bigint): Uint8Array => {
  const nonce = new Uint8Array(12)
  new DataView(nonce.buffer).setBigUint64(4, n, true)
  return nonce
}

export class CipherState {
  private n = 0n
  constructor(private readonly key: Uint8Array) {}

  encrypt(plaintext: Uint8Array, ad: Uint8Array = empty): Uint8Array {
    const out = chacha20poly1305(this.key, nonceBytes(this.n), ad).encrypt(plaintext)
    this.n += 1n
    return out
  }

  decrypt(ciphertext: Uint8Array, ad: Uint8Array = empty): Uint8Array {
    const out = chacha20poly1305(this.key, nonceBytes(this.n), ad).decrypt(ciphertext)
    this.n += 1n
    return out
  }
}

const hkdf = (chainingKey: Uint8Array, ikm: Uint8Array, outputs: 2 | 3): Array<Uint8Array> => {
  const tempKey = hmac(sha256, chainingKey, ikm)
  const out1 = hmac(sha256, tempKey, Uint8Array.of(1))
  const out2 = hmac(sha256, tempKey, concat(out1, Uint8Array.of(2)))
  if (outputs === 2) return [out1, out2]
  return [out1, out2, hmac(sha256, tempKey, concat(out2, Uint8Array.of(3)))]
}

class SymmetricState {
  private h: Uint8Array
  private ck: Uint8Array
  private cipher: CipherState | undefined

  constructor(protocolName: string) {
    const name = encoder.encode(protocolName)
    this.h = name.length <= 32 ? concat(name, new Uint8Array(32 - name.length)) : sha256(name)
    this.ck = this.h
  }

  mixHash(data: Uint8Array): void {
    this.h = sha256(concat(this.h, data))
  }

  mixKey(ikm: Uint8Array): void {
    const [ck, key] = hkdf(this.ck, ikm, 2)
    this.ck = ck!
    this.cipher = new CipherState(key!)
  }

  mixKeyAndHash(ikm: Uint8Array): void {
    const [ck, tempH, key] = hkdf(this.ck, ikm, 3)
    this.ck = ck!
    this.mixHash(tempH!)
    this.cipher = new CipherState(key!)
  }

  encryptAndHash(plaintext: Uint8Array): Uint8Array {
    const out = this.cipher ? this.cipher.encrypt(plaintext, this.h) : plaintext
    this.mixHash(out)
    return out
  }

  decryptAndHash(ciphertext: Uint8Array): Uint8Array {
    const out = this.cipher ? this.cipher.decrypt(ciphertext, this.h) : ciphertext
    this.mixHash(ciphertext)
    return out
  }

  split(): { send: CipherState; recv: CipherState } {
    const [k1, k2] = hkdf(this.ck, empty, 2)
    return { send: new CipherState(k1!), recv: new CipherState(k2!) }
  }
}

export type Transport = { send: CipherState; recv: CipherState }

export type InitiatorConfig = {
  kind: HandshakeKind
  staticPrivate: Uint8Array
  homeStaticPublic: Uint8Array
  deviceSignPub: Uint8Array
  psk?: Uint8Array
  ephemeralPrivate?: Uint8Array
}

const dhLen = 32
const tagLen = 16

export const startHandshake = (config: InitiatorConfig, payload: Uint8Array) => {
  const usesPsk = config.kind === HandshakeKind.pair
  if (usesPsk && config.psk?.length !== 32) {
    throw new Error(`pairing needs a 32-byte secret from the QR code; got ${config.psk?.length ?? 0} bytes`)
  }
  const state = new SymmetricState(
    usesPsk ? 'Noise_IKpsk2_25519_ChaChaPoly_SHA256' : 'Noise_IK_25519_ChaChaPoly_SHA256',
  )
  state.mixHash(prologue(config.deviceSignPub))
  state.mixHash(config.homeStaticPublic)

  const ephemeralPrivate = config.ephemeralPrivate ?? x25519.utils.randomSecretKey()
  const ephemeralPublic = x25519.getPublicKey(ephemeralPrivate)
  state.mixHash(ephemeralPublic)
  if (usesPsk) state.mixKey(ephemeralPublic)
  state.mixKey(x25519.getSharedSecret(ephemeralPrivate, config.homeStaticPublic))
  const sealedStatic = state.encryptAndHash(x25519.getPublicKey(config.staticPrivate))
  state.mixKey(x25519.getSharedSecret(config.staticPrivate, config.homeStaticPublic))
  const sealedPayload = state.encryptAndHash(payload)
  const firstFrame = concat(Uint8Array.of(config.kind), ephemeralPublic, sealedStatic, sealedPayload)

  const finish = (reply: Uint8Array): Transport => {
    if (reply.length < dhLen + tagLen) {
      throw new Error(
        `the home's handshake reply is ${reply.length} bytes; a Noise reply is at least ${dhLen + tagLen}`,
      )
    }
    const homeEphemeral = reply.subarray(0, dhLen)
    state.mixHash(homeEphemeral)
    if (usesPsk) state.mixKey(homeEphemeral)
    state.mixKey(x25519.getSharedSecret(ephemeralPrivate, homeEphemeral))
    state.mixKey(x25519.getSharedSecret(config.staticPrivate, homeEphemeral))
    if (usesPsk) state.mixKeyAndHash(config.psk!)
    state.decryptAndHash(reply.subarray(dhLen))
    return state.split()
  }

  return { firstFrame, finish }
}
