import { hmac } from '@noble/hashes/hmac.js'
import { sha256 } from '@noble/hashes/sha2.js'
import { open, seal, x25519PublicKey, x25519SharedSecret } from './appleCrypto'

export const HandshakeKind = { paired: 1, pair: 2 } as const
export type HandshakeKind = (typeof HandshakeKind)[keyof typeof HandshakeKind]

const encoder = new TextEncoder()
const empty = new Uint8Array(0)

export const prologue = (phoneSignPub: Uint8Array): Uint8Array =>
  concat(encoder.encode('attn-cloud/v1 noise\n'), phoneSignPub)

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

  encrypt(plaintext: Uint8Array, ad: Uint8Array = empty): Promise<Uint8Array> {
    return seal(this.key, nonceBytes(this.n++), ad, plaintext)
  }

  decrypt(ciphertext: Uint8Array, ad: Uint8Array = empty): Promise<Uint8Array> {
    return open(this.key, nonceBytes(this.n++), ad, ciphertext)
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

  async encryptAndHash(plaintext: Uint8Array): Promise<Uint8Array> {
    const out = this.cipher ? await this.cipher.encrypt(plaintext, this.h) : plaintext
    this.mixHash(out)
    return out
  }

  async decryptAndHash(ciphertext: Uint8Array): Promise<Uint8Array> {
    const out = this.cipher ? await this.cipher.decrypt(ciphertext, this.h) : ciphertext
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
  phoneSignPub: Uint8Array
  psk?: Uint8Array
  ephemeralPrivate?: Uint8Array
}

const dhLen = 32
const tagLen = 16

export const startHandshake = async (config: InitiatorConfig, payload: Uint8Array) => {
  const usesPsk = config.kind === HandshakeKind.pair
  if (usesPsk && config.psk?.length !== 32) {
    throw new Error(`pairing needs a 32-byte secret from the QR code; got ${config.psk?.length ?? 0} bytes`)
  }
  const state = new SymmetricState(
    usesPsk ? 'Noise_IKpsk2_25519_ChaChaPoly_SHA256' : 'Noise_IK_25519_ChaChaPoly_SHA256',
  )
  state.mixHash(prologue(config.phoneSignPub))
  state.mixHash(config.homeStaticPublic)

  const ephemeralPrivate = config.ephemeralPrivate ?? crypto.getRandomValues(new Uint8Array(32))
  const ephemeralPublic = await x25519PublicKey(ephemeralPrivate)
  state.mixHash(ephemeralPublic)
  if (usesPsk) state.mixKey(ephemeralPublic)
  state.mixKey(await x25519SharedSecret(ephemeralPrivate, config.homeStaticPublic))
  const sealedStatic = await state.encryptAndHash(await x25519PublicKey(config.staticPrivate))
  state.mixKey(await x25519SharedSecret(config.staticPrivate, config.homeStaticPublic))
  const sealedPayload = await state.encryptAndHash(payload)
  const firstFrame = concat(Uint8Array.of(config.kind), ephemeralPublic, sealedStatic, sealedPayload)

  const finish = async (reply: Uint8Array): Promise<Transport> => {
    if (reply.length < dhLen + tagLen) {
      throw new Error(
        `the home's handshake reply is ${reply.length} bytes; a Noise reply is at least ${dhLen + tagLen}`,
      )
    }
    const homeEphemeral = reply.subarray(0, dhLen)
    state.mixHash(homeEphemeral)
    if (usesPsk) state.mixKey(homeEphemeral)
    state.mixKey(await x25519SharedSecret(ephemeralPrivate, homeEphemeral))
    state.mixKey(await x25519SharedSecret(config.staticPrivate, homeEphemeral))
    if (usesPsk) state.mixKeyAndHash(config.psk!)
    await state.decryptAndHash(reply.subarray(dhLen))
    return state.split()
  }

  return { firstFrame, finish }
}
