import { useEffect } from 'react';
import { MarkdownReader } from '../../src/components/MarkdownReader';
import { fileMarkdownSource } from '../../src/components/MarkdownReader/documentSource';
import type { HarnessProps } from '../types';

const DOCUMENT_SOURCE = fileMarkdownSource('/tmp/markdown-reader-width-harness.md');

const DOCUMENT = `---
type: note
title: Domain entities
sources:
  - https://github.com/example/project/blob/main/packages/accounts-payable/src/legacy/ap-state-machine.ts
  - https://github.com/example/project/blob/main/packages/v2/agent-conversations-contract/src/agent-conversation.ts
---

# Domain entities

This note gives the main aggregates in each bounded context, how they relate, and what states they move through. It was read from main on the day it was written, and every paragraph here is long enough to wrap at the reading measure on a wide tile.

- Payables and receivables produce journal entries, and the subledger keeps their open balances for every entity.
- Banking moves the money and settles them.

| Context | Aggregate | Stored status | Derived status | Owner | Notes |
| --- | --- | --- | --- | --- | --- |
| Payables | bill | draft, approved, paid | overdue, partially paid, scheduled for payment | accounts-payable | The legacy state machine still drives approvals for older bills |

\`\`\`ts
export const billStatus = (bill: Bill, payments: readonly Payment[]): BillStatus => deriveStatus(bill.storedStatus, payments.filter((payment) => payment.billId === bill.id));
\`\`\`
`;

export function MarkdownReaderWidthHarness({ onReady }: HarnessProps) {
  const width = Number(new URLSearchParams(window.location.search).get('width') ?? 1800);

  useEffect(() => {
    onReady();
  }, [onReady]);

  return (
    <div role="region" aria-label="Markdown tile" style={{ width, padding: 12, boxSizing: 'border-box' }}>
      <MarkdownReader content={DOCUMENT} source={DOCUMENT_SOURCE} />
    </div>
  );
}
