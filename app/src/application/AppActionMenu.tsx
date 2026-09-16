import { ActionMenu } from '../components/ActionMenu';
import { useAppPanelsContext } from './AppContexts';
import { useAppActionItems } from './useAppActionItems';
export function AppActionMenu() {
  const { actionMenuOpen, setActionMenuOpen } = useAppPanelsContext();
  const items = useAppActionItems();
  return (
    <ActionMenu isOpen={actionMenuOpen} actions={items} onClose={() => setActionMenuOpen(false)} />
  );
}
