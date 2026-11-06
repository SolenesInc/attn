export function stillFrameOf(canvas: HTMLCanvasElement): HTMLCanvasElement | null {
  if (canvas.width === 0 || canvas.height === 0) return null;
  const still = document.createElement('canvas');
  still.width = canvas.width;
  still.height = canvas.height;
  still.style.cssText = canvas.style.cssText;
  still.getContext('2d')?.drawImage(canvas, 0, 0);
  return still;
}
