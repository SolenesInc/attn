import { chacha20poly1305 } from '@noble/ciphers/chacha.js'
import { x25519 } from '@noble/curves/ed25519.js'

export const x25519PublicKey = async (privateKey: Uint8Array) => x25519.getPublicKey(privateKey)

export const x25519SharedSecret = async (privateKey: Uint8Array, publicKey: Uint8Array) =>
  x25519.getSharedSecret(privateKey, publicKey)

export const seal = async (key: Uint8Array, nonce: Uint8Array, ad: Uint8Array, plaintext: Uint8Array) =>
  chacha20poly1305(key, nonce, ad).encrypt(plaintext)

let heldOpens: Array<() => void> | undefined

export const holdDecryption = () => {
  heldOpens = []
  return () => {
    const held = heldOpens ?? []
    heldOpens = undefined
    for (const release of held) release()
  }
}

export const open = async (key: Uint8Array, nonce: Uint8Array, ad: Uint8Array, sealed: Uint8Array) => {
  if (heldOpens) await new Promise<void>((release) => heldOpens!.push(release))
  return chacha20poly1305(key, nonce, ad).decrypt(sealed)
}
