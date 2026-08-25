// @vitest-environment node
// Marker parity with internal/pty/osc133_test.go over the shared corpus is the contract.
// @types/node isn't a direct dependency here (only a transitive peer of vite/vitest).
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { emptyOsc133State, parseOsc133, type Osc133Marker } from './terminalOsc133';

interface CorpusCase {
  name: string;
  chunks: string[];
  markers: Array<{
    kind: Osc133Marker['kind'];
    cmdline?: string;
    exitCode?: number;
  }>;
}

const corpusPath = fileURLToPath(
  new URL('../../../internal/pty/testdata/osc133_segmenter_corpus.json', import.meta.url),
);
const corpus = JSON.parse(readFileSync(corpusPath, 'utf8')) as { cases: CorpusCase[] };

const encoder = new TextEncoder();

function collectMarkers(chunks: string[]): Osc133Marker[] {
  let state = emptyOsc133State();
  const markers: Osc133Marker[] = [];
  for (const chunk of chunks) {
    const result = parseOsc133(state, encoder.encode(chunk));
    state = result.state;
    for (const segment of result.segments) {
      if (segment.marker) markers.push(segment.marker);
    }
  }
  return markers;
}

function normalize(marker: Osc133Marker): CorpusCase['markers'][number] {
  const out: CorpusCase['markers'][number] = { kind: marker.kind };
  if (marker.kind === 'pre-exec' && marker.cmdline !== undefined) out.cmdline = marker.cmdline;
  if (marker.kind === 'command-end' && marker.exitCode !== undefined) out.exitCode = marker.exitCode;
  return out;
}

describe('parseOsc133 segmenter parity corpus', () => {
  it('covers every case', () => {
    expect(corpus.cases.length).toBeGreaterThan(0);
  });

  for (const testCase of corpus.cases) {
    it(testCase.name, () => {
      const markers = collectMarkers(testCase.chunks).map(normalize);
      expect(markers).toEqual(testCase.markers);
    });
  }
});
