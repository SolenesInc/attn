import { ActionMenu } from '../components/ActionMenu';
import { useAppContext } from './AppContext';
import { useAppActionItems } from './useAppActionItems';
export function AppActionMenu() {
  const { actionMenuOpen, setActionMenuOpen } = useAppContext();
  const items = useAppActionItems();
  return (
    <ActionMenu isOpen={actionMenuOpen} actions={items} onClose={() => setActionMenuOpen(false)} />
  );
}
