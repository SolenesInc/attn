import { useState } from 'react';
import type { MigrationFailure } from '../utils/migrationFailure';
import { writeClipboardText } from '../utils/clipboardBridge';
import './MigrationFailureScreen.css';

function failureReport(failure: MigrationFailure): string {
  return [
    'attn failed to migrate its data at startup.',
    `Failure marker: ${failure.markerPath}`,
    ...failure.facts.map((fact) => `${fact.label}: ${fact.value}`),
  ].join('\n');
}

export function MigrationFailureScreen({ failure }: { failure: MigrationFailure }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void writeClipboardText(failureReport(failure)).then(() => setCopied(true));
  };
  return (
    <main className="migration-failure" data-testid="migration-failure-screen">
      <h1>attn seems broken after migration</h1>
      <p>
        Ask an agent outside attn to investigate, preferably one running from within the attn
        codebase. Give it the details below.
      </p>
      <dl className="migration-failure-facts">
        {failure.facts.map((fact) => (
          <div key={fact.label} className="migration-failure-fact">
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
        <div className="migration-failure-fact">
          <dt>Failure marker</dt>
          <dd>{failure.markerPath}</dd>
        </div>
      </dl>
      <button type="button" className="migration-failure-copy" onClick={copy}>
        {copied ? 'Copied' : 'Copy details'}
      </button>
    </main>
  );
}
