import type { HomeGatewayAskImageChunk } from '../homeGateway/generated'
import type { Link } from '../link'

export const chunkWindow = 4

// A failure the home reported keeps failing; a dropped connection is worth another try once the home is back.
export class ImageRefused extends Error {}

const decodeBase64 = (data: string) => Uint8Array.from(atob(data), (c) => c.charCodeAt(0))

const readChunk = async (
  link: Link,
  askId: string,
  src: string,
  offset: number,
): Promise<HomeGatewayAskImageChunk> => {
  const reply = await link.request({ cmd: 'ask_image', ask_id: askId, src, offset })
  if (reply.event === 'ask_image_error') throw new ImageRefused(reply.message)
  if (reply.event !== 'ask_image_chunk') throw new Error(`the home answered ${reply.event} to ask_image`)
  return reply
}

export const fetchAskImage = async (link: Link, askId: string, src: string): Promise<Blob> => {
  const first = await readChunk(link, askId, src, 0)
  const firstBytes = decodeBase64(first.data_base64)
  const image = new Uint8Array(first.size)
  image.set(firstBytes)
  const offsets: Array<number> = []
  if (first.size > firstBytes.length) {
    if (firstBytes.length === 0) throw new Error(`the home sent an empty first chunk of ${src}`)
    for (let offset = firstBytes.length; offset < first.size; offset += firstBytes.length)
      offsets.push(offset)
  }
  let next = 0
  const reader = async () => {
    while (next < offsets.length) {
      const offset = offsets[next++]!
      image.set(decodeBase64((await readChunk(link, askId, src, offset)).data_base64), offset)
    }
  }
  await Promise.all(Array.from({ length: Math.min(chunkWindow, offsets.length) }, reader))
  return new Blob([image], { type: first.mime_type })
}
