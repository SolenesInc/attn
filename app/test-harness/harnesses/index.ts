import { SidebarTreeHarness } from './SidebarTreeHarness';
import type { HarnessProps } from '../types';
import { QuickCaptureHistoryHarness } from './QuickCaptureHistoryHarness';
import { BrokenLinksHarness } from './BrokenLinksHarness';
import { FileTreeHarness } from './FileTreeHarness';
import { FrontmatterCardHarness } from './FrontmatterCardHarness';
import { LiveMarkdownEditorHarness } from './LiveMarkdownEditorHarness';
import { MarkdownAnnotationTilesHarness } from './MarkdownAnnotationTilesHarness';
import { MermaidDiagramHarness } from './MermaidDiagramHarness';
import { NotebookBrowserHarness } from './NotebookBrowserHarness';
import { NotebookTileHarness } from './NotebookTileHarness';
import { OutpostTerminalLinksHarness } from './OutpostTerminalLinksHarness';
import { PaneFocusRingHarness } from './PaneFocusRingHarness';
import { QueueBarHarness } from './QueueBarHarness';
import { QueueSidebarFitHarness } from './QueueSidebarFitHarness';
import { TileHeaderHarness } from './TileHeaderHarness';
import { SeedHeaderHarness } from './SeedHeaderHarness';
import { TerminalAnnotationsHarness } from './TerminalAnnotationsHarness';
import { DelegationChainHarness } from './DelegationChainHarness';
import { AgentHeaderHarness } from './AgentHeaderHarness';
import { SidebarRailHarness } from './SidebarRailHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  QuickCaptureHistory: QuickCaptureHistoryHarness,
  AgentHeader: AgentHeaderHarness,
  BrokenLinks: BrokenLinksHarness,
  FileTree: FileTreeHarness,
  FrontmatterCard: FrontmatterCardHarness,
  LiveMarkdownEditor: LiveMarkdownEditorHarness,
  MarkdownAnnotationTiles: MarkdownAnnotationTilesHarness,
  MermaidDiagram: MermaidDiagramHarness,
  NotebookBrowser: NotebookBrowserHarness,
  NotebookTile: NotebookTileHarness,
  OutpostTerminalLinks: OutpostTerminalLinksHarness,
  PaneFocusRing: PaneFocusRingHarness,
  TileHeader: TileHeaderHarness,
  SeedHeader: SeedHeaderHarness,
  TerminalAnnotations: TerminalAnnotationsHarness,
  DelegationChain: DelegationChainHarness,
  QueueBar: QueueBarHarness,
  QueueSidebarFit: QueueSidebarFitHarness,
  SidebarRail: SidebarRailHarness,
  SidebarTree: SidebarTreeHarness,
};
