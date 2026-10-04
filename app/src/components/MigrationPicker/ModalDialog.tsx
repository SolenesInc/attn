import { useLayoutEffect, useRef, type KeyboardEvent, type ReactNode } from 'react';
import { useToastHost } from '../../utils/toastHost';
import { useEscapeStack } from '../../hooks/useEscapeStack';

interface ModalDialogProps {
  labelledBy: string;
  onCancel: () => void;
  onKeyDown?: (event: KeyboardEvent<HTMLDialogElement>) => void;
  children: ReactNode;
}

export function ModalDialog({ labelledBy, onCancel, onKeyDown, children }: ModalDialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const hostRef = useToastHost(true, ref);
  useEscapeStack(onCancel, true);
  useLayoutEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) dialog.showModal();
    return () => dialog?.close();
  }, []);
  return (
    <dialog
      ref={hostRef}
      className="mp-dialog"
      aria-labelledby={labelledBy}
      onCancel={(event) => event.preventDefault()}
      onKeyDown={onKeyDown}
    >
      {children}
    </dialog>
  );
}
