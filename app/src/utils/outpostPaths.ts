export function outpostPathRefusal(path: string, hostName: string): string {
  return `${path} is on ${hostName}; opening files on an outpost isn't supported yet`;
}

export function localDirectory(session: { cwd?: string; host?: string } | undefined): string | undefined {
  return session && !session.host ? session.cwd : undefined;
}
