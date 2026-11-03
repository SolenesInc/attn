import { CapacitorHttp, registerPlugin } from '@capacitor/core'
import { ed25519 } from '@noble/curves/ed25519.js'
import { toBase64Url } from './bytes'
import { deviceKeys } from './device'

export type ApnsEnvironment = 'sandbox' | 'production'

type AttnPushPlugin = {
  prepare(options: { noiseSecret: string }): Promise<{ environment: ApnsEnvironment }>
}

const AttnPush = registerPlugin<AttnPushPlugin>('AttnPush')

export type PushHome = { readonly cloud: string; readonly homeId: string }

export type PushTap = {
  readonly homeId: string
  readonly profileId: string
  readonly sessionId: string
  readonly askId: string
}

export type PushToken = { readonly token: string; readonly environment: ApnsEnvironment }

export const notificationsOff =
  'Notifications are off for attn, so waiting agents cannot reach you. Turn them on in Settings → Notifications → attn.'

export const watchPushTokens = async (
  onToken: (token: PushToken) => void,
  onRefused: (message: string) => void,
): Promise<() => Promise<void>> => {
  const { PushNotifications } = await import('@capacitor/push-notifications')
  const keys = await deviceKeys()
  const { environment } = await AttnPush.prepare({ noiseSecret: toBase64Url(keys.noiseSecret) })
  let permission = await PushNotifications.checkPermissions()
  if (permission.receive === 'prompt' || permission.receive === 'prompt-with-rationale') {
    permission = await PushNotifications.requestPermissions()
  }
  if (permission.receive !== 'granted') {
    onRefused(notificationsOff)
    return async () => {}
  }
  const handles = await Promise.all([
    PushNotifications.addListener('registration', ({ value }) =>
      onToken({ token: value.toLowerCase(), environment }),
    ),
    PushNotifications.addListener('registrationError', ({ error }) =>
      onRefused(`iOS gave attn no push token, so waiting agents cannot reach you: ${error}`),
    ),
  ])
  await PushNotifications.register()
  return async () => {
    await Promise.all(handles.map((handle) => handle.remove()))
  }
}

export const pushTokenMessage = (homeId: string, devicePub: string, environment: string, token: string) =>
  new TextEncoder().encode(`attn-cloud/v1 push-token\n${homeId}\n${devicePub}\n${environment}\n${token}`)

export const registerForPush = async (home: PushHome, { token, environment }: PushToken): Promise<void> => {
  const keys = await deviceKeys()
  const devicePub = toBase64Url(keys.signPub)
  const response = await CapacitorHttp.post({
    url: `${home.cloud}/v1/homes/${home.homeId}/push-token`,
    headers: { 'content-type': 'application/json' },
    data: {
      device_sign_pub: devicePub,
      platform: 'apns',
      environment,
      token,
      sig: toBase64Url(
        ed25519.sign(pushTokenMessage(home.homeId, devicePub, environment, token), keys.signSecret),
      ),
    },
  })
  if (response.status !== 200) {
    const refusal = response.data as { error?: { message?: string } } | null
    throw new Error(
      `Attn Cloud refused this phone's push token (${response.status}): ${refusal?.error?.message ?? 'no reason given'}`,
    )
  }
}

export const readPushTap = (data: unknown): PushTap | null => {
  if (typeof data !== 'object' || data === null) return null
  const { home_id, profile_id, session_id, ask_id } = data as Record<string, unknown>
  if (typeof home_id !== 'string' || typeof profile_id !== 'string' || typeof session_id !== 'string')
    return null
  return {
    homeId: home_id,
    profileId: profile_id,
    sessionId: session_id,
    askId: typeof ask_id === 'string' ? ask_id : '',
  }
}
