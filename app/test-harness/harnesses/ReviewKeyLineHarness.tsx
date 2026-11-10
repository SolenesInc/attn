import { useEffect } from 'react';
import { ReviewKeyLine } from '../../src/components/ReviewReader/ReviewKeys';
import type { HarnessProps } from '../types';
import '../../src/App.css';
import '../../src/components/ReviewReader/ReviewReader.css';

const navigation = [
  { language: 'go', state: 'ready' },
  { language: 'rust', state: 'starting', detail: 'Indexing the workspace' },
  { language: 'zig', state: 'missing', detail: 'zls is not on PATH' },
];

export function ReviewKeyLineHarness({ onReady, setTriggerRerender }: HarnessProps) {
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  const width = Number(new URLSearchParams(window.location.search).get('width')) || 360;
  return (
    <div className="md-reader--review" data-testid="review-key-line" style={{ width, background: 'var(--color-bg-app)' }}>
      <ReviewKeyLine frame="brief" offered={['brief', 'walkthrough', 'map']} fileOpen={false} sides={false} marking navigation={navigation} onFrame={() => {}} />
    </div>
  );
}
