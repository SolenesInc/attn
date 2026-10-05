import { useEffect, useState } from 'react';
import '../../src/App.css';
import { MarkdownReader } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import { setMarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { HarnessProps } from '../types';

const SOURCE = fileMarkdownSource('test-harness', '/tmp/markdown-annotation-tiles.md');
const DOCUMENT = '# Plan\n\nFirst paragraph with target words inside it.\n\nSecond block of plain prose here.\n';

export function MarkdownAnnotationTilesHarness({ onReady }: HarnessProps) {
  const fragments = new URLSearchParams(window.location.search).get('document') === 'fragments';
  const content = fragments ? '[Jump](#setup)\n\n' + 'A paragraph before the destination.\n\n'.repeat(20) + '## Setup\n\nThe destination.\n' : DOCUMENT;
  const tileStyle = { flex: 1, position: 'relative' as const, ...(fragments ? { height: 300, overflowY: 'auto' as const } : {}) };
  const [secondOpen, setSecondOpen] = useState(true);

  useEffect(() => {
    setMarkdownAnnotationsTransport({
      getMarkdownAnnotations: async () => ({ annotations: [], generation: 0 }),
      saveMarkdownAnnotations: async () => ({ stale: false }),
      clearMarkdownAnnotations: async (_source, generation) => ({ generation }),
      submitMarkdownAnnotations: async () => ({ status: 'delivered' }),
    });
    onReady();
    return () => setMarkdownAnnotationsTransport(null);
  }, [onReady]);

  return (
    <div style={{ display: 'flex', gap: 16, padding: 12 }}>
      <section aria-label="First tile" className={fragments ? 'desktop-dock-tile-body' : undefined} style={tileStyle}>
        <MarkdownReader content={content} source={SOURCE} annotationsEnabled={!fragments} />
      </section>
      {secondOpen ? (
        <section aria-label="Second tile" className={fragments ? 'desktop-dock-tile-body' : undefined} style={tileStyle}>
          <MarkdownReader content={content} source={SOURCE} annotationsEnabled={!fragments} />
        </section>
      ) : null}
      <button type="button" onClick={() => setSecondOpen(false)}>Close second tile</button>
    </div>
  );
}
