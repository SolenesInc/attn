import type { CSSProperties, ReactNode } from 'react';
import './SidePanel.css';

interface SidePanelProps {
  isOpen: boolean;
  position?: 'absolute' | 'fixed';
  width?: string;
  offsetRight?: string;
  className?: string;
  children: ReactNode;
}

export function SidePanel({
  isOpen,
  position = 'absolute',
  width,
  offsetRight,
  className = '',
  children,
}: SidePanelProps) {
  const style = {
    ...(width ? { ['--side-panel-width' as string]: width } : {}),
    ...(offsetRight ? { ['--side-panel-offset' as string]: offsetRight } : {}),
  } as CSSProperties | undefined;

  return (
    <div className={`side-panel-shell side-panel-shell--${position} ${isOpen ? 'is-open' : 'is-closed'}`}>
      <aside
        className={`side-panel ${className}`.trim()}
        style={style}
        aria-hidden={!isOpen}
        inert={!isOpen || undefined}
      >
        {children}
      </aside>
    </div>
  );
}
