// Pixel layouts of a stored kitty image, indexed by wire code, mirroring
// kittyImageFormatNames in internal/protocol/binaryframe.go.

export type KittyPixelFormat = 'rgb' | 'rgba' | 'gray_alpha' | 'gray';

const FORMATS_BY_CODE: readonly KittyPixelFormat[] = ['rgb', 'rgba', 'gray_alpha', 'gray'];

export const KITTY_FORMAT_BYTES_PER_PIXEL: Readonly<Record<KittyPixelFormat, number>> = {
  rgb: 3,
  rgba: 4,
  gray_alpha: 2,
  gray: 1,
};

export function kittyPixelFormatFromCode(code: number): KittyPixelFormat | null {
  return FORMATS_BY_CODE[code] ?? null;
}

export function kittyPixelFormatFromName(name: string): KittyPixelFormat | null {
  return (FORMATS_BY_CODE as readonly string[]).includes(name)
    ? (name as KittyPixelFormat)
    : null;
}
