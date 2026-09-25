import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderSettings } from './test/settings';

function endpointCard(name: string) {
  return within(screen.getByText(name, { selector: '.endpoint-name' }).closest<HTMLElement>('.endpoint-card')!);
}

describe('App endpoint actions', () => {
  it('holds every endpoint action while one is in flight, so updates never collide', async () => {
    const { daemon } = await renderSettings({
      endpoints: [
        { id: 'ep-1', name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'connected', enabled: true },
        { id: 'ep-2', name: 'build-box', ssh_target: 'user@build-box', status: 'connected', enabled: true },
      ],
    });

    fireEvent.click(endpointCard('gpu-box').getByRole('button', { name: 'Disable' }));
    const update = await daemon.received('update_endpoint');
    expect(update).toMatchObject({ endpoint_id: 'ep-1', enabled: false });
    expect(endpointCard('build-box').getByRole('button', { name: 'Disable' })).toBeDisabled();

    daemon.emit({ event: 'endpoint_action_result', action: 'update', endpoint_id: 'ep-1', success: true });
    await daemon.idle();

    expect(endpointCard('build-box').getByRole('button', { name: 'Disable' })).toBeEnabled();
    expect(daemon.sentOf('update_endpoint')).toHaveLength(1);
  });
});
