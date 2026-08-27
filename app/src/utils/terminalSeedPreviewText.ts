export function terminalSeedBodyExcerpt(body: string, maxLength = 260): string {
  const plain = body
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/^\s*(?:[-*+] |\d+\. )/gm, '')
    .replace(/[*_~`>|]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  if (plain.length <= maxLength) return plain;
  const clipped = plain.slice(0, maxLength + 1);
  const boundary = clipped.lastIndexOf(' ');
  return `${clipped.slice(0, boundary > maxLength * 0.65 ? boundary : maxLength).trimEnd()}…`;
}
