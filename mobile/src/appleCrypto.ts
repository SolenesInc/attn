import { registerPlugin } from '@capacitor/core'
import { fromBase64, toBase64 } from './bytes'

type AppleCryptoPlugin = {
  x25519PublicKey(options: { privateKey: string }): Promise<{ value: string }>
  x25519SharedSecret(options: { privateKey: string; publicKey: string }): Promise<{ value: string }>
  seal(options: { key: string; nonce: string; ad: string; plaintext: string }): Promise<{ value: string }>
  open(options: { key: string; nonce: string; ad: string; sealed: string }): Promise<{ value: string }>
}

const AppleCrypto = registerPlugin<AppleCryptoPlugin>('AppleCrypto')

export const x25519PublicKey = async (privateKey: Uint8Array): Promise<Uint8Array> =>
  fromBase64((await AppleCrypto.x25519PublicKey({ privateKey: toBase64(privateKey) })).value)

export const x25519SharedSecret = async (
  privateKey: Uint8Array,
  publicKey: Uint8Array,
): Promise<Uint8Array> =>
  fromBase64(
    (
      await AppleCrypto.x25519SharedSecret({
        privateKey: toBase64(privateKey),
        publicKey: toBase64(publicKey),
      })
    ).value,
  )

export const seal = async (
  key: Uint8Array,
  nonce: Uint8Array,
  ad: Uint8Array,
  plaintext: Uint8Array,
): Promise<Uint8Array> =>
  fromBase64(
    (
      await AppleCrypto.seal({
        key: toBase64(key),
        nonce: toBase64(nonce),
        ad: toBase64(ad),
        plaintext: toBase64(plaintext),
      })
    ).value,
  )

export const open = async (
  key: Uint8Array,
  nonce: Uint8Array,
  ad: Uint8Array,
  sealed: Uint8Array,
): Promise<Uint8Array> =>
  fromBase64(
    (
      await AppleCrypto.open({
        key: toBase64(key),
        nonce: toBase64(nonce),
        ad: toBase64(ad),
        sealed: toBase64(sealed),
      })
    ).value,
  )
