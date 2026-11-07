import { readFileSync } from 'node:fs'
import { hexToBytes, bytesToHex } from '@noble/hashes/utils.js'
import { describe, expect, it } from 'vitest'
import { HandshakeKind, startHandshake } from './noise'

type Vector = {
  name: string
  kind: number
  home_static_public: string
  device_static_private: string
  device_ephemeral_private: string
  device_sign_pub: string
  psk?: string
  first_payload: string
  device_first_frame: string
  home_reply: string
  device_to_home_plaintext: string
  device_to_home: string
  home_to_device_plaintext: string
  home_to_device: string
}

const vectors: Array<Vector> = JSON.parse(
  readFileSync(new URL('../../internal/cloudlink/testdata/noise_vectors.json', import.meta.url), 'utf8'),
)
const text = new TextEncoder()

describe('the Noise initiator speaks the home responder vectors', () => {
  it.each(vectors.map((vector) => [vector.name, vector] as const))('%s', async (_, vector) => {
    const kind = vector.kind === 2 ? HandshakeKind.pair : HandshakeKind.paired
    const { firstFrame, finish } = await startHandshake(
      {
        kind,
        staticPrivate: hexToBytes(vector.device_static_private),
        homeStaticPublic: hexToBytes(vector.home_static_public),
        phoneSignPub: hexToBytes(vector.device_sign_pub),
        ...(vector.psk ? { psk: hexToBytes(vector.psk) } : {}),
        ephemeralPrivate: hexToBytes(vector.device_ephemeral_private),
      },
      text.encode(vector.first_payload),
    )
    expect(bytesToHex(firstFrame)).toBe(vector.device_first_frame)

    const transport = await finish(hexToBytes(vector.home_reply))
    expect(bytesToHex(await transport.send.encrypt(text.encode(vector.device_to_home_plaintext)))).toBe(
      vector.device_to_home,
    )
    expect(new TextDecoder().decode(await transport.recv.decrypt(hexToBytes(vector.home_to_device)))).toBe(
      vector.home_to_device_plaintext,
    )
  })

  it('refuses a home reply forged without the pairing secret', async () => {
    const vector = vectors.find((each) => each.kind === 2)!
    const { finish } = await startHandshake(
      {
        kind: HandshakeKind.pair,
        staticPrivate: hexToBytes(vector.device_static_private),
        homeStaticPublic: hexToBytes(vector.home_static_public),
        phoneSignPub: hexToBytes(vector.device_sign_pub),
        psk: new Uint8Array(32).fill(7),
        ephemeralPrivate: hexToBytes(vector.device_ephemeral_private),
      },
      text.encode(vector.first_payload),
    )
    await expect(finish(hexToBytes(vector.home_reply))).rejects.toThrow()
  })
})
