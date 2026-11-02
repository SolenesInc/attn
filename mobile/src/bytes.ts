export const toBase64Url = (bytes: Uint8Array): string =>
  btoa(String.fromCharCode(...bytes))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '')

export const fromBase64Url = (text: string): Uint8Array =>
  Uint8Array.from(atob(text.replaceAll('-', '+').replaceAll('_', '/')), (char) => char.charCodeAt(0))

const base32Alphabet = 'abcdefghijklmnopqrstuvwxyz234567'

export const toBase32 = (bytes: Uint8Array): string => {
  let out = ''
  let buffer = 0
  let bits = 0
  for (const byte of bytes) {
    buffer = (buffer << 8) | byte
    bits += 8
    while (bits >= 5) {
      out += base32Alphabet[(buffer >>> (bits - 5)) & 31]
      bits -= 5
    }
  }
  if (bits > 0) out += base32Alphabet[(buffer << (5 - bits)) & 31]
  return out
}
