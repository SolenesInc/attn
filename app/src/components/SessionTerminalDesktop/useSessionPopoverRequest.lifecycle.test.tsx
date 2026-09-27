import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useSessionPopoverRequest } from './useSessionPopoverRequest';

describe('session popover requests', () => {
  it('keeps a dismissed request closed and accepts a new request for the same session', () => {
    const request = { sessionId: 'agent', nonce: 1 };
    const { result, rerender } = renderHook(useSessionPopoverRequest, { initialProps: request });
    expect(result.current[0]).toBe('agent');
    act(() => result.current[1]());
    rerender(request);
    expect(result.current[0]).toBeNull();
    rerender({ sessionId: 'agent', nonce: 2 });
    expect(result.current[0]).toBe('agent');
    rerender({ sessionId: 'other-agent', nonce: 3 });
    expect(result.current[0]).toBe('other-agent');
  });
});
