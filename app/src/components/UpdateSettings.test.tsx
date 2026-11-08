import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { UpdateSettings } from './UpdateSettings';

describe('UpdateSettings', () => {
  it('shows the daemon reported install, build and available release', () => {
    render(<UpdateSettings settings={{ update_install_kind: 'release_app', update_current_version: '0.13.0', update_current_commit: 'abcdef1234567', update_available_version: '0.14.0' }} />);
    expect(screen.getByText('Release app')).toBeInTheDocument();
    expect(screen.getByText('0.13.0 (abcdef123)')).toBeInTheDocument();
    expect(screen.getByText('0.14.0')).toBeInTheDocument();
  });

  it('shows why availability is unknown after a failed check', () => {
    render(<UpdateSettings settings={{ update_install_kind: 'release_binary', update_current_version: '0.13.0', update_current_commit: 'abcdef1', update_check_error: 'network is down' }} />);
    expect(screen.getByText('Unknown. The last check failed: network is down')).toBeInTheDocument();
  });
});
