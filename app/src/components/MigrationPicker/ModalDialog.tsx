import { useLayoutEffect, useRef, type KeyboardEvent, type ReactNode } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';

interface ModalDialogProps {
  labelledBy: string;
  onCancel: () => void;
  onKeyDown?: (event: KeyboardEvent<HTMLDialogElement>) => void;
  children: ReactNode;
}

export function ModalDialog({ labelledBy, onCancel, onKeyDown, children }: ModalDialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  useEscapeStack(onCancel, true);
  useLayoutEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) dialog.showModal();
    return () => dialog?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className="mp-dialog"
      aria-labelledby={labelledBy}
      onCancel={(event) => event.preventDefault()}
      onKeyDown={onKeyDown}
    >
      {children}
    </dialog>
  );
}
