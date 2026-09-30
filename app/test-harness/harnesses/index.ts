import type { HarnessProps } from '../types';
import { BrokenLinksHarness } from './BrokenLinksHarness';
import { DiffViewHarness } from './DiffViewHarness';
import { FileTreeHarness } from './FileTreeHarness';
import { FrontmatterCardHarness } from './FrontmatterCardHarness';
import { GridLayoutControlHarness } from './GridLayoutControlHarness';
import { GridViewHarness } from './GridViewHarness';
import { LiveMarkdownEditorHarness } from './LiveMarkdownEditorHarness';
import { MarkdownAnnotationTilesHarness } from './MarkdownAnnotationTilesHarness';
import { MermaidDiagramHarness } from './MermaidDiagramHarness';
import { NotebookBrowserHarness } from './NotebookBrowserHarness';
import { NotebookTileHarness } from './NotebookTileHarness';
import { PaneFocusRingHarness } from './PaneFocusRingHarness';
import { PresentTourHarness } from './PresentTourHarness';
import { QueueBarHarness } from './QueueBarHarness';
import { QueueSidebarFitHarness } from './QueueSidebarFitHarness';
import { TileHeaderHarness } from './TileHeaderHarness';
import { SeedHeaderHarness } from './SeedHeaderHarness';
import { TerminalAnnotationsHarness } from './TerminalAnnotationsHarness';
import { DelegationChainHarness } from './DelegationChainHarness';
import { AgentHeaderHarness } from './AgentHeaderHarness';
import { SidebarRailHarness } from './SidebarRailHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  AgentHeader: AgentHeaderHarness,
  BrokenLinks: BrokenLinksHarness,
  DiffView: DiffViewHarness,
  FileTree: FileTreeHarness,
  FrontmatterCard: FrontmatterCardHarness,
  GridLayoutControl: GridLayoutControlHarness,
  GridView: GridViewHarness,
  LiveMarkdownEditor: LiveMarkdownEditorHarness,
  MarkdownAnnotationTiles: MarkdownAnnotationTilesHarness,
  MermaidDiagram: MermaidDiagramHarness,
  NotebookBrowser: NotebookBrowserHarness,
  NotebookTile: NotebookTileHarness,
  PaneFocusRing: PaneFocusRingHarness,
  PresentTour: PresentTourHarness,
  TileHeader: TileHeaderHarness,
  SeedHeader: SeedHeaderHarness,
  TerminalAnnotations: TerminalAnnotationsHarness,
  DelegationChain: DelegationChainHarness,
  QueueBar: QueueBarHarness,
  QueueSidebarFit: QueueSidebarFitHarness,
  SidebarRail: SidebarRailHarness,
};
