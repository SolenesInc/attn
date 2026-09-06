import type { HarnessProps } from '../types';
import { BridgeSettledReadHarness } from './BridgeSettledReadHarness';
import { BrokenLinksHarness } from './BrokenLinksHarness';
import { DashboardPRsHarness } from './DashboardPRsHarness';
import { DiffViewHarness } from './DiffViewHarness';
import { FileTreeHarness } from './FileTreeHarness';
import { FrontmatterCardHarness } from './FrontmatterCardHarness';
import { GridLayoutControlHarness } from './GridLayoutControlHarness';
import { GridViewHarness } from './GridViewHarness';
import { LiveMarkdownEditorHarness } from './LiveMarkdownEditorHarness';
import { MermaidDiagramHarness } from './MermaidDiagramHarness';
import { NeedsHumanHarness } from './NeedsHumanHarness';
import { NotebookBrowserHarness } from './NotebookBrowserHarness';
import { NotebookTileHarness } from './NotebookTileHarness';
import { PaneFocusRingHarness } from './PaneFocusRingHarness';
import { PresentTourHarness } from './PresentTourHarness';
import { TileHeaderHarness } from './TileHeaderHarness';
import { SeedHeaderHarness } from './SeedHeaderHarness';

export const harnesses: Record<string, React.ComponentType<HarnessProps>> = {
  BridgeSettledRead: BridgeSettledReadHarness,
  BrokenLinks: BrokenLinksHarness,
  DashboardPRs: DashboardPRsHarness,
  DiffView: DiffViewHarness,
  FileTree: FileTreeHarness,
  FrontmatterCard: FrontmatterCardHarness,
  GridLayoutControl: GridLayoutControlHarness,
  GridView: GridViewHarness,
  LiveMarkdownEditor: LiveMarkdownEditorHarness,
  MermaidDiagram: MermaidDiagramHarness,
  NeedsHuman: NeedsHumanHarness,
  NotebookBrowser: NotebookBrowserHarness,
  NotebookTile: NotebookTileHarness,
  PaneFocusRing: PaneFocusRingHarness,
  PresentTour: PresentTourHarness,
  TileHeader: TileHeaderHarness,
  SeedHeader: SeedHeaderHarness,
};
