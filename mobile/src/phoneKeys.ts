import { Capacitor, registerPlugin } from '@capacitor/core'
import { ed25519 } from '@noble/curves/ed25519.js'
import { hkdf } from '@noble/hashes/hkdf.js'
import { sha256 } from '@noble/hashes/sha2.js'
import { fromBase64Url, toBase32, toBase64Url } from './bytes'

type KeychainPlugin = {
  get(options: { key: string }): Promise<{ value?: string }>
  set(options: { key: string; value: string }): Promise<void>
}

const Keychain = registerPlugin<KeychainPlugin>('Keychain', {
  web: () => ({
    get: async ({ key }: { key: string }) => {
      const value = localStorage.getItem(`attn-keychain:${key}`)
      return value === null ? {} : { value }
    },
    set: async ({ key, value }: { key: string; value: string }) => {
      localStorage.setItem(`attn-keychain:${key}`, value)
    },
  }),
})

export type PhoneKeys = {
  readonly signSecret: Uint8Array
  readonly signPub: Uint8Array
  readonly noiseSecret: Uint8Array
  readonly id: string
  readonly name: string
}

const keychainKey = 'device-keys'

const fromSecrets = (signSecret: Uint8Array, noiseSecret: Uint8Array): PhoneKeys => {
  const signPub = ed25519.getPublicKey(signSecret)
  return {
    signSecret,
    signPub,
    noiseSecret,
    id: toBase32(sha256(signPub).subarray(0, 16)),
    name: Capacitor.getPlatform() === 'ios' ? 'iPhone' : 'Browser',
  }
}

let loading: Promise<PhoneKeys> | undefined

export const phoneKeys = (): Promise<PhoneKeys> =>
  (loading ??= loadOrCreate().catch((error: unknown) => {
    loading = undefined
    throw error
  }))

const loadOrCreate = async (): Promise<PhoneKeys> => {
  const stored = await Keychain.get({ key: keychainKey })
  if (stored.value) {
    const { sign, noise } = JSON.parse(stored.value) as { sign: string; noise: string }
    return fromSecrets(fromBase64Url(sign), fromBase64Url(noise))
  }
  const signSecret = ed25519.utils.randomSecretKey()
  const noiseSecret = crypto.getRandomValues(new Uint8Array(32))
  await Keychain.set({
    key: keychainKey,
    value: JSON.stringify({ sign: toBase64Url(signSecret), noise: toBase64Url(noiseSecret) }),
  })
  return fromSecrets(signSecret, noiseSecret)
}

export const signChallenge = (keys: PhoneKeys, homeId: string, nonce: Uint8Array): Uint8Array => {
  const prefix = new TextEncoder().encode(`attn-cloud/v1 auth device\n${homeId}\n`)
  const message = new Uint8Array(prefix.length + nonce.length)
  message.set(prefix)
  message.set(nonce, prefix.length)
  return ed25519.sign(message, keys.signSecret)
}

export const relayToken = (qrSecret: Uint8Array): Uint8Array =>
  hkdf(sha256, qrSecret, undefined, new TextEncoder().encode('attn-cloud/v1 relay token'), 32)
