import { Preferences } from '@capacitor/preferences'
import { Schema, Struct } from 'effect'

export const PairingCode = Schema.Struct({
  v: Schema.Literal(1),
  cloud: Schema.String,
  home_id: Schema.String,
  home_name: Schema.String,
  home_noise_pub: Schema.String,
  profile_id: Schema.String,
  profile_name: Schema.String,
  pairing_id: Schema.String,
  secret: Schema.String,
})
export type PairingCode = typeof PairingCode.Type

export const Pairing = Schema.Struct({
  id: Schema.String,
  cloud: Schema.String,
  homeId: Schema.String,
  homeNoisePub: Schema.String,
  homeName: Schema.String,
  profileId: Schema.String,
  profileName: Schema.String,
  pairedAt: Schema.Finite,
})
export type Pairing = typeof Pairing.Type

export const LinkTarget = Pairing.mapFields(Struct.omit(['profileName']))
export type LinkTarget = typeof LinkTarget.Type

export const linkTarget = (pairing: Pairing): LinkTarget => Struct.omit(pairing, ['profileName'])

export const pairingId = (homeId: string, profileId: string): string => `${homeId}/${profileId}`

export const readPairingCode = (scanned: string): PairingCode => {
  let json: unknown
  try {
    json = JSON.parse(scanned)
  } catch {
    throw new Error(
      'That QR code is not an attn pairing code. Open Settings → Phones on your computer and scan the code it shows.',
    )
  }
  try {
    return Schema.decodeUnknownSync(PairingCode)(json)
  } catch (error) {
    throw new Error(`That pairing code is not one this app reads: ${String(error)}`)
  }
}

export const pairingFromCode = (
  pairingCode: PairingCode,
  homeName: string,
  profileName: string,
  pairedAt: number,
): Pairing => ({
  id: pairingId(pairingCode.home_id, pairingCode.profile_id),
  cloud: pairingCode.cloud,
  homeId: pairingCode.home_id,
  homeNoisePub: pairingCode.home_noise_pub,
  homeName,
  profileName,
  profileId: pairingCode.profile_id,
  pairedAt,
})

const storageKey = 'pairings'
const StoredPairings = Schema.Array(Pairing)

export const loadPairings = async (): Promise<ReadonlyArray<Pairing>> => {
  const { value } = await Preferences.get({ key: storageKey })
  return value ? Schema.decodeUnknownSync(StoredPairings)(JSON.parse(value)) : []
}

export const savePairings = (pairings: ReadonlyArray<Pairing>): Promise<void> =>
  Preferences.set({ key: storageKey, value: JSON.stringify(pairings) })
