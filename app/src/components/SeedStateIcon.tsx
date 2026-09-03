import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import './SeedStateIcon.css';

const states: Record<string, { label: string; meaning: string; drawing: ReactNode }> = {
  planted: {
    label: 'Planted',
    meaning: 'Open, not currently being tended.',
    drawing: (
      <>
        <path className="soil" d="M5 25.5c3-1.1 5-1.1 7 0m8 0c2-1.1 4-1.1 7 0" />
        <g className="seed-body">
          <path className="wash" d="M20.7 9.6c2.6 3.8.7 10-4.2 12.1-3.8 1.7-6.9-.4-6-4.5 1-4.6 6.4-8.5 10.2-7.6Z" />
          <path className="fine" d="M18 12.6c-2.6 1.3-4.3 3.7-4.6 6.3" />
        </g>
        <g className="soil soil-grains">
          <path d="M9 28h2m10 0h2" />
          <circle cx="16" cy="27.5" r=".65" className="solid" />
        </g>
      </>
    ),
  },
  growing: {
    label: 'Growing',
    meaning: 'Claimed and being worked on.',
    drawing: (
      <>
        <path className="soil" d="M7 28h18" />
        <path className="stem" d="M16 27V14" />
        <g className="leaf-left">
          <path className="wash" d="M15.9 20C9.6 20.3 6.1 17 6.5 11.7c5.5-.6 9.4 2.5 9.4 8.3Z" />
          <path className="fine" d="m10 15 5.8 5" />
        </g>
        <g className="leaf-right">
          <path className="wash" d="M16 16c.4-6.6 4.2-10 10-9.6.5 5.7-3.4 9.8-10 9.6Z" />
          <path className="fine" d="m16.3 15.7 6-5.8" />
        </g>
      </>
    ),
  },
  dormant: {
    label: 'Dormant',
    meaning: 'Paused. Tending it again resumes the work.',
    drawing: (
      <>
        <path className="soil" d="M7 28h18" />
        <path d="M16 27v-3" />
        <g className="sleeping-bud">
          <path className="wash" d="M16 11.5c-5.6.5-8.6 5.1-6.8 9.1 1.9 4.1 11.2 4.5 13.3.4 2.1-4-1-8.8-6.5-9.5Z" />
          <path className="fine" d="M16 11.7c-.7 2.4.3 4 2.7 4.7" />
          <path d="M11.5 18.8q1.7 1.6 3.4 0m3 .2q1.3 1.2 2.6-.2" />
        </g>
        <path className="moon wash" d="M24.8 3.8c-1 3.8.9 5.6 4 5-1.2 3.7-6 4-7.5.9-1.2-2.6.5-5.2 3.5-5.9Z" />
        <path className="sleep-mote" d="M5.2 9.3h2.6l-2.6 2.3h2.6" />
      </>
    ),
  },
  harvested: {
    label: 'Harvested',
    meaning: 'The work is complete.',
    drawing: (
      <>
        <path className="fine" d="M10 15v-3a6 6 0 0 1 12 0v3" />
        <g className="berry-left">
          <circle className="wash" cx="10.7" cy="14" r="3.4" />
          <path className="fine" d="m11 10.5-.8-1.8" />
        </g>
        <g className="berry-center">
          <circle className="wash" cx="16.4" cy="11.9" r="3.4" />
          <path className="wash fine" d="M16.3 8.4c.4-2.4 2-3.1 3.5-2.5-.3 2-1.6 2.8-3.5 2.5Z" />
        </g>
        <g className="berry-right">
          <circle className="wash" cx="22" cy="14" r="3.3" />
        </g>
        <g className="basket">
          <path className="wash" d="M6.3 17.1h19.4l-2.4 10H8.7l-2.4-10Z" />
          <path d="M5.5 16.9h21" />
          <path className="fine" d="m11.3 19.9.8 4.5m3.9-4.5v4.5m4.7-4.5-.8 4.5M9.5 22.1h13" />
        </g>
        <path className="fleck" d="M4.4 9v3m-1.5-1.5h3M27.1 4.5v2m-1-1h2" />
      </>
    ),
  },
  withered: {
    label: 'Withered',
    meaning: 'Closed without a harvest.',
    drawing: (
      <>
        <path className="soil" d="M6 28.5h12" />
        <g className="drooping-stem">
          <path d="M14 27.5c.8-4.7 2.1-8.5 2.1-13.1 0-4.8 5.9-7.4 8.5-3.7" />
          <path className="wash" d="M15.4 21c-5 .2-8.2-2.8-8.1-6.9 4.9.3 7.6 2.7 8.1 6.9Z" />
          <path className="fine" d="m10.4 17.3 4.8 3.5" />
          <path className="wash" d="M24.6 10.7c-4.1 1.1-5 4.1-1.4 7.2 3.7-1.8 4.3-4.8 1.4-7.2Z" />
        </g>
        <g className="fallen-leaf">
          <path className="wash" d="M20 27.8c1.7-3.6 5.3-4.7 8.3-2.5-1.8 3.4-5.2 4.3-8.3 2.5Z" />
          <path className="fine" d="m21 27.4 4.7-1.4" />
        </g>
      </>
    ),
  },
};

export function seedStateLabel(status?: string): string {
  return states[status ?? '']?.label ?? 'Unknown';
}

export function seedStateMeaning(status?: string): string {
  return states[status ?? '']?.meaning ?? 'Seed details are not available yet.';
}

export function SeedStateIcon({ status, size = 24, replay = 0 }: {
  status?: string;
  size?: number;
  replay?: number;
}) {
  const [playing, setPlaying] = useState(false);
  useEffect(() => {
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    setPlaying(true);
    const timer = window.setTimeout(() => setPlaying(false), 1500);
    return () => window.clearTimeout(timer);
  }, [status, replay]);

  return (
    <svg
      className={`seed-state-icon${playing ? ' is-playing' : ''}`}
      data-seed-state={status}
      width={size}
      height={size}
      viewBox="0 0 32 32"
      aria-hidden="true"
      focusable="false"
    >
      {states[status ?? '']?.drawing ?? <circle cx="16" cy="16" r="8" strokeDasharray="2 4" />}
    </svg>
  );
}

export function SeedPlotIcon() {
  return (
    <svg className="seed-state-icon seed-plot-icon" width="24" height="24" viewBox="0 0 32 32" aria-hidden="true" focusable="false">
      <path className="wash" d="m4 20 12-5 12 5-12 7-12-7Z" />
      <path className="soil" d="M4 24.5 16 31l12-6.5" />
      <path d="M11 20v-8m0 4c-4 0-5-3-5-5 4 0 5 2 5 5Zm0-3c0-4 2-6 6-6 0 4-2 6-6 6Zm10 8v-7m0 3c0-4 2-5 5-5 0 3-2 5-5 5Z" />
    </svg>
  );
}
