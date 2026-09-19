import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import type { CrewMember } from '../types/generated';
import type { CrewCharterAutosave } from './useCrewCharterAutosave';

export type CrewTab = 'launch' | 'charter' | 'handoffs' | 'seeds';

export type CrewNavigation =
  | { kind: 'member'; memberId: string; rosterIndex?: number }
  | { kind: 'tab'; tab: CrewTab }
  | { kind: 'seed'; seedId: string }
  | { kind: 'close' };

export interface CrewNavigationState {
  selectedMember?: CrewMember;
  tab: CrewTab;
  pending: boolean;
  navigate: (intent: CrewNavigation) => void;
}

export function useCrewNavigation({ members, initialMember, rosterRef, charter, onClose, onOpenSeed }: {
  members: CrewMember[];
  initialMember?: string;
  rosterRef: RefObject<HTMLDivElement | null>;
  charter: CrewCharterAutosave;
  onClose: () => void;
  onOpenSeed: (seedId: string, placementSessionId?: string) => void;
}): CrewNavigationState {
  const [selectedId, setSelectedId] = useState(() => (
    initialMember && members.some((candidate) => candidate.id === initialMember) ? initialMember : members[0]?.id || ''
  ));
  const [tab, setTab] = useState<CrewTab>('launch');
  const [pending, setPending] = useState(false);
  const sequence = useRef(0);
  const handlers = useRef({ onClose, onOpenSeed });
  useEffect(() => {
    handlers.current = { onClose, onOpenSeed };
  }, [onClose, onOpenSeed]);

  const selectedMember = members.find((member) => member.id === selectedId) ?? members[0];
  const selectedMemberId = selectedMember?.id;
  const bindingSession = selectedMember?.binding_session;
  const charterEdit = selectedMemberId ? charter.read(selectedMemberId) : undefined;
  const charterUnsettled = tab === 'charter' && Boolean(charterEdit?.acknowledged)
    && charterEdit?.state !== 'saved' && charterEdit?.state !== 'error' && charterEdit?.state !== 'conflict';

  const navigate = useCallback((intent: CrewNavigation) => {
    const current = ++sequence.current;
    const apply = () => {
      switch (intent.kind) {
        case 'member':
          setSelectedId(intent.memberId);
          if (intent.rosterIndex !== undefined) {
            rosterRef.current?.querySelectorAll<HTMLButtonElement>('[data-crew-roster-member]')[intent.rosterIndex]?.focus();
          }
          return;
        case 'tab':
          setTab(intent.tab);
          return;
        case 'seed':
          handlers.current.onOpenSeed(intent.seedId, bindingSession);
          return;
        case 'close':
          handlers.current.onClose();
      }
    };
    if (!charterUnsettled || !selectedMemberId) {
      apply();
      return;
    }
    setPending(true);
    void charter.flush(selectedMemberId).then((saved) => {
      if (saved && sequence.current === current) apply();
    }).finally(() => {
      if (sequence.current === current) setPending(false);
    });
  }, [bindingSession, charter, charterUnsettled, rosterRef, selectedMemberId]);

  return { selectedMember, tab, pending, navigate };
}
