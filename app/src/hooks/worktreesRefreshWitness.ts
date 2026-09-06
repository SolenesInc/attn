export interface RefreshWitnessReading {
  mutations: number;
  sawRefreshing: boolean;
  peakRefreshing: number;
  firstSeenMs: number | null;
  lastSeenMs: number | null;
}

interface ArmedWitness extends RefreshWitnessReading {
  observer: MutationObserver;
  armedAt: number;
}

let armed: ArmedWitness | null = null;

function countIn(root: ParentNode, selector: string): number {
  return root.querySelectorAll(selector).length;
}

// A node added and removed inside one batch is gone by the time the callback
// runs, so the added nodes are counted as well as what is on screen now.
function countInRecords(records: MutationRecord[], selector: string): number {
  let count = 0;
  for (const record of records) {
    for (const node of Array.from(record.addedNodes)) {
      if (!(node instanceof Element)) continue;
      if (node.matches(selector)) count += 1;
      count += countIn(node, selector);
    }
  }
  return count;
}

export function armRefreshWitness(panel: HTMLElement, selector: string): RefreshWitnessReading {
  disarmRefreshWitness();

  const witness: ArmedWitness = {
    observer: new MutationObserver((records) => {
      const seen = Math.max(countIn(panel, selector), countInRecords(records, selector));
      witness.mutations += records.length;
      if (seen <= 0) return;
      const at = Date.now() - witness.armedAt;
      witness.sawRefreshing = true;
      witness.peakRefreshing = Math.max(witness.peakRefreshing, seen);
      if (witness.firstSeenMs === null) witness.firstSeenMs = at;
      witness.lastSeenMs = at;
    }),
    armedAt: Date.now(),
    mutations: 0,
    sawRefreshing: false,
    peakRefreshing: 0,
    firstSeenMs: null,
    lastSeenMs: null,
  };

  const present = countIn(panel, selector);
  if (present > 0) {
    witness.sawRefreshing = true;
    witness.peakRefreshing = present;
    witness.firstSeenMs = 0;
    witness.lastSeenMs = 0;
  }

  witness.observer.observe(panel, { subtree: true, childList: true, attributes: true });
  armed = witness;
  return readRefreshWitness() as RefreshWitnessReading;
}

export function readRefreshWitness(): RefreshWitnessReading | null {
  if (!armed) return null;
  const { mutations, sawRefreshing, peakRefreshing, firstSeenMs, lastSeenMs } = armed;
  return { mutations, sawRefreshing, peakRefreshing, firstSeenMs, lastSeenMs };
}

export function disarmRefreshWitness(): void {
  armed?.observer.disconnect();
  armed = null;
}
