import { beforeEach, describe, expect, it } from 'vitest';
import {
  inputTraceId,
  observeDocumentInput,
  recordTerminalInputTrace,
  recordTransportInputTrace,
  resetFrontendInputTraceForTests,
  snapshotFrontendInputTrace,
} from './supportInputTrace';

describe('support input trace', () => {
  beforeEach(() => resetFrontendInputTraceForTests());

  it('carries one content-free id from the document through transport', () => {
    const event = new KeyboardEvent('keydown', {
      key: 'PRIVATE_KEY', code: 'KeyP', metaKey: true, bubbles: true,
    });
    const traceId = observeDocumentInput(event, 'terminal');
    expect(inputTraceId(event)).toBe(traceId);
    recordTerminalInputTrace(traceId, {
      event: 'keydown', outcome: 'sent', runtimeId: 'runtime-1', paneId: 'pane-1', modifiers: 8,
    });
    recordTransportInputTrace(traceId, {
      event: 'websocket_send', runtimeId: 'runtime-1', socketState: 1, initialStateReceived: true,
    });

    const snapshot = snapshotFrontendInputTrace();
    expect(snapshot.events).toHaveLength(3);
    expect(snapshot.events.map((entry) => entry.traceId)).toEqual([traceId, traceId, traceId]);
    expect(snapshot.events.map((entry) => entry.stage)).toEqual(['document', 'terminal', 'transport']);
    expect(JSON.stringify(snapshot)).not.toContain('PRIVATE_KEY');
    expect(JSON.stringify(snapshot)).not.toContain('KeyP');
  });

  it('retains only the newest fixed-capacity window', () => {
    for (let index = 1; index <= 520; index += 1) {
      recordTerminalInputTrace(`trace-${index}`, { event: 'keydown', outcome: 'sent' });
    }

    const snapshot = snapshotFrontendInputTrace();
    expect(snapshot.capacity).toBe(512);
    expect(snapshot.total).toBe(520);
    expect(snapshot.events).toHaveLength(512);
    expect(snapshot.events[0]?.sequence).toBe(9);
    expect(snapshot.events[snapshot.events.length - 1]?.sequence).toBe(520);
  });
});
