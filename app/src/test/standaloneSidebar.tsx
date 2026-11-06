import { Sidebar, SidebarProvider } from '../components/Sidebar';
import type { SidebarProps } from '../components/sidebarTypes';

export function StandaloneSidebar(props: SidebarProps) {
  return <SidebarProvider {...props}><Sidebar /></SidebarProvider>;
}
