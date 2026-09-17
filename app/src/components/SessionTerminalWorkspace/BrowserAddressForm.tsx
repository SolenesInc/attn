import type { FormEvent } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { browserHostLabel, controlBrowserHost } from '../../browser/host';
import { type TileLeaf } from '../../types/workspace';
import { normalizeBrowserAddress } from './browserAddress';

interface Props {
  tile: TileLeaf;
  workspaceId: string;
  onUpdateParams?: (params: string) => Promise<unknown> | void;
}
export function BrowserAddressForm({ tile, workspaceId, onUpdateParams }: Props) {
  const browserLabel = browserHostLabel(workspaceId, tile.tileId);
  const [address, setAddress] = useState({
    persisted: tile.tileParams,
    value: tile.tileParams || '',
  });
  if (address.persisted !== tile.tileParams)
    setAddress({ persisted: tile.tileParams, value: tile.tileParams || '' });
  const browserAddress = address.value;
  const setBrowserAddress = useCallback(
    (value: string) => setAddress({ persisted: tile.tileParams, value }),
    [tile.tileParams],
  );
  const pendingBrowserParamsRef = useRef<string | null>(null);
  useEffect(() => {
    if (pendingBrowserParamsRef.current === tile.tileParams) {
      pendingBrowserParamsRef.current = null;
    }
  }, [tile.tileParams]);

  useEffect(() => {
    if (tile.tileKind !== 'browser') {
      return;
    }
    const handleLocation = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail;
      if (
        typeof detail === 'object' &&
        detail !== null &&
        'label' in detail &&
        'url' in detail &&
        detail.label === browserLabel &&
        typeof detail.url === 'string'
      ) {
        setBrowserAddress(detail.url);
        if (detail.url !== tile.tileParams && detail.url !== pendingBrowserParamsRef.current) {
          pendingBrowserParamsRef.current = detail.url;
          void Promise.resolve(onUpdateParams?.(detail.url)).catch((error) => {
            if (pendingBrowserParamsRef.current === detail.url) {
              pendingBrowserParamsRef.current = null;
            }
            console.warn('[WorkspaceDockTile] Failed to persist browser location:', error);
          });
        }
      }
    };
    window.addEventListener('attn:browser-location', handleLocation);
    return () => {
      window.removeEventListener('attn:browser-location', handleLocation);
    };
  }, [browserLabel, onUpdateParams, tile.tileKind, tile.tileParams, setBrowserAddress]);

  const navigateBrowser = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmed = browserAddress.trim();
    if (!trimmed) {
      return;
    }
    const target = normalizeBrowserAddress(trimmed);
    setBrowserAddress(target);
    void controlBrowserHost(
      workspaceId,
      tile.tileId,
      'navigate',
      JSON.stringify({ url: target }),
    ).catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to navigate browser:', error);
    });
  };

  return (
    <form
      className="workspace-dock-tile-address-form"
      onSubmit={navigateBrowser}
      onPointerDown={(event) => event.stopPropagation()}
    >
      <input
        className="workspace-dock-tile-address"
        type="text"
        value={browserAddress}
        aria-label="Browser address"
        spellCheck={false}
        onChange={(event) => setBrowserAddress(event.target.value)}
        onFocus={(event) => event.currentTarget.select()}
      />
    </form>
  );
}
