// The global dispatcher runs in the capture phase and always sees ⌘P first, so a
// surface registers a claim here rather than racing it with a container keydown.

interface PaletteClaim {
  // Resolved at claim time: a ref's element is null on the render that registers it.
  container: () => HTMLElement | null;
  open: () => void;
}

const claims = new Set<PaletteClaim>();

export function registerPaletteClaim(claim: PaletteClaim): () => void {
  claims.add(claim);
  return () => { claims.delete(claim); };
}

export function claimPaletteFocus(active: Element | null = document.activeElement): boolean {
  if (!active) return false;
  for (const claim of claims) {
    const container = claim.container();
    if (container && container.contains(active)) {
      claim.open();
      return true;
    }
  }
  return false;
}
