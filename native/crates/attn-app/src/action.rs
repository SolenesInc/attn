/// What the cheat sheet groups an action under.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Category {
    Sessions,
    Panes,
    Markdown,
    Review,
    App,
}

impl Category {
    pub fn label(self) -> &'static str {
        match self {
            Category::Sessions => "Desktops & Sessions",
            Category::Panes => "Panes & Terminals",
            Category::Markdown => "Markdown & Annotations",
            Category::Review => "Review & Git",
            Category::App => "App",
        }
    }
}

macro_rules! actions {
    ($($variant:ident = $id:literal, $category:ident, $label:literal;)*) => {
        /// Everything a person can ask the app to do by key, menu or palette. A unit variant is
        /// one the keymap can bind: its id is the shortcut id the daemon's `keybindings_config`
        /// setting keys rebinds by, shared with the Tauri app. A variant with data names what
        /// it acts on, as a palette row does, and no key binds it.
        #[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
        pub enum Action {
            $($variant,)*
            /// Go to an agent: its desktop and its tile.
            OpenAgent { session: String },
            /// Start a session for a crew member between sessions.
            StartCrewMember { member: String },
            /// Go to a tile that is not an agent, on its desktop.
            OpenTile { desktop: String, tile: String },
            GoToDesktop { desktop: String },
            /// Move the active tile and its delegates to a desktop.
            MoveWithDelegates { desktop: String },
            SwitchToProfile { profile: String },
            /// Rename an agent, in the rename popover over its tile.
            RenameSession { session: String },
            /// Pin the usage breakdown in an agent's tile header.
            ShowSessionUsage { session: String },
            /// Pin the delegation chain in an agent's tile header.
            ShowDelegationChain { session: String },
            /// Pin the seeds an agent tends, and the one it reports to, in its tile header.
            ShowSessionSeeds { session: String },
            /// Restart an agent in its pane.
            ReloadSession { session: String },
            /// End an agent's snooze.
            WakeSession { session: String },
            /// Store a daemon setting.
            SetSetting { key: String, value: String },
            /// Forget every column width dragged on a desktop.
            ClearDraggedWidths { desktop: String },
            /// Order a profile's desktops by number, then by name.
            SortDesktops { profile: String },
            /// Put a profile's desktops in this order, as Undo after a sort does.
            SetDesktopOrder { profile: String, desktops: Vec<String> },
            /// Close a desktop, asking first when it holds tiles.
            CloseDesktop { desktop: String },
            /// Open the asks drawer on an ask, or on the first one.
            ShowAsks { ask: Option<String> },
            /// Open the ledger on its Worktrees list.
            OpenWorktrees,
            /// Open the notifications panel, or close it.
            ToggleNotifications,
            /// Open the editor that rebinds shortcuts.
            OpenShortcutEditor,
            /// Ask the daemon to update attn.
            StartUpdate,
            /// Open the attention drawer and take the keyboard into it.
            OpenAttention,
            /// Replay the tour of what this release brings.
            OpenWhatsNew,
        }

        impl Action {
            /// The actions a key can bind.
            pub const ALL: &[Action] = &[$(Action::$variant,)*];

            pub fn id(&self) -> &'static str {
                match self {
                    $(Action::$variant => $id,)*
                    Action::OpenAgent { .. } => "agent.open",
                    Action::StartCrewMember { .. } => "crew.start",
                    Action::OpenTile { .. } => "tile.open",
                    Action::GoToDesktop { .. } => "desktop.goTo",
                    Action::MoveWithDelegates { .. } => "desktop.moveWithDelegates",
                    Action::SwitchToProfile { .. } => "profile.switchTo",
                    Action::RenameSession { .. } => "session.rename",
                    Action::ReloadSession { .. } => "session.reload",
                    Action::ShowSessionUsage { .. } => "session.showUsage",
                    Action::ShowDelegationChain { .. } => "session.showDelegationChain",
                    Action::ShowSessionSeeds { .. } => "session.showSeeds",
                    Action::WakeSession { .. } => "session.wake",
                    Action::SetSetting { .. } => "setting.set",
                    Action::ClearDraggedWidths { .. } => "shelf.clearDraggedWidths",
                    Action::SortDesktops { .. } => "desktop.sort",
                    Action::SetDesktopOrder { .. } => "desktop.setOrder",
                    Action::CloseDesktop { .. } => "desktop.close",
                    Action::ShowAsks { .. } => "asks.show",
                    Action::OpenWorktrees => "ledger.worktrees",
                    Action::ToggleNotifications => "notifications.toggle",
                    Action::OpenShortcutEditor => "shortcuts.edit",
                    Action::StartUpdate => "update.start",
                    Action::OpenAttention => "attention.open",
                    Action::OpenWhatsNew => "whatsNew.open",
                }
            }

            pub fn label(&self) -> &'static str {
                match self {
                    $(Action::$variant => $label,)*
                    Action::OpenAgent { .. } => "Go to an agent",
                    Action::StartCrewMember { .. } => "Start a crew member",
                    Action::OpenTile { .. } => "Go to a tile",
                    Action::GoToDesktop { .. } => "Go to a desktop",
                    Action::MoveWithDelegates { .. } => "Move with delegates to a desktop",
                    Action::SwitchToProfile { .. } => "Switch to a profile",
                    Action::RenameSession { .. } => "Rename an agent",
                    Action::ReloadSession { .. } => "Reload an agent",
                    Action::ShowSessionUsage { .. } => "Show an agent's usage",
                    Action::ShowDelegationChain { .. } => "Show an agent's delegation chain",
                    Action::ShowSessionSeeds { .. } => "Show an agent's seeds",
                    Action::WakeSession { .. } => "Unsnooze an agent",
                    Action::SetSetting { .. } => "Change a setting",
                    Action::ClearDraggedWidths { .. } => "Clear dragged widths",
                    Action::SortDesktops { .. } => "Sort desktops",
                    Action::SetDesktopOrder { .. } => "Restore an order of desktops",
                    Action::CloseDesktop { .. } => "Close desktop",
                    Action::ShowAsks { .. } => "Open asks",
                    Action::OpenWorktrees => "Open worktrees",
                    Action::ToggleNotifications => "Show or hide notifications",
                    Action::OpenShortcutEditor => "Customize keyboard shortcuts",
                    Action::StartUpdate => "Update attn",
                    Action::OpenAttention => "Open attention drawer",
                    Action::OpenWhatsNew => "What's new",
                }
            }

            pub fn category(&self) -> Category {
                match self {
                    $(Action::$variant => Category::$category,)*
                    _ => Category::Sessions,
                }
            }

            /// The bindable action with this id.
            pub fn from_id(id: &str) -> Option<Action> {
                match id {
                    $($id => Some(Action::$variant),)*
                    _ => None,
                }
            }
        }
    };
}

actions! {
    SessionNew = "session.new", Sessions, "New session on this desktop";
    SessionNewTerminal = "session.newTerminal", Sessions, "New terminal on this desktop";
    SessionNewHorizontal = "session.newHorizontal", Sessions, "New session below";
    SessionClose = "session.close", Sessions, "Close session, focused pane or empty desktop";
    SessionPrev = "session.prev", Sessions, "Previous desktop (queue mode: previous agent in the queue)";
    SessionNext = "session.next", Sessions, "Next desktop (queue mode: next agent in the queue)";
    SessionHistoryBack = "session.historyBack", Sessions, "Back through agent history";
    SessionHistoryForward = "session.historyForward", Sessions, "Forward through agent history";
    SessionOrchestrator = "session.orchestrator", Sessions, "Jump to this session's dispatcher";
    SessionGoToDashboard = "session.goToDashboard", Sessions, "Go to dashboard (home)";
    DesktopOverview = "desktop.overview", Sessions, "Desktop overview";
    ProfileSwitch = "profile.switch", Sessions, "Switch profile";
    SessionJumpToWaiting = "session.jumpToWaiting", Sessions, "Jump to next waiting session";
    SessionNextRun = "session.nextRun", Sessions, "Next automation run needing you";
    SidebarAgentList = "sidebar.agentList", Sessions, "All agents";
    SidebarWider = "sidebar.wider", Sessions, "Widen the sidebar";
    SidebarNarrower = "sidebar.narrower", Sessions, "Narrow the sidebar";
    SessionSettle = "session.settle", Sessions, "Settle turn";
    DockAsks = "dock.asks", Sessions, "Asks drawer";
    SessionPriority = "session.priority", Sessions, "Toggle session priority";
    SessionSnooze = "session.snooze", Sessions, "Snooze this agent";
    SessionCancelCountdown = "session.cancelCountdown", Sessions, "Stop the countdown, or keep the next turn";
    SessionToggleSidebar = "session.toggleSidebar", Sessions, "Toggle sidebar";
    DesktopSelect1 = "desktop.select1", Sessions, "Switch to desktop 1";
    DesktopSelect2 = "desktop.select2", Sessions, "Switch to desktop 2";
    DesktopSelect3 = "desktop.select3", Sessions, "Switch to desktop 3";
    DesktopSelect4 = "desktop.select4", Sessions, "Switch to desktop 4";
    DesktopSelect5 = "desktop.select5", Sessions, "Switch to desktop 5";
    DesktopSelect6 = "desktop.select6", Sessions, "Switch to desktop 6";
    DesktopSelect7 = "desktop.select7", Sessions, "Switch to desktop 7";
    DesktopSelect8 = "desktop.select8", Sessions, "Switch to desktop 8";
    DesktopSelect9 = "desktop.select9", Sessions, "Switch to desktop 9";
    DesktopSend1 = "desktop.send1", Sessions, "Move this to desktop 1 and follow";
    DesktopSend2 = "desktop.send2", Sessions, "Move this to desktop 2 and follow";
    DesktopSend3 = "desktop.send3", Sessions, "Move this to desktop 3 and follow";
    DesktopSend4 = "desktop.send4", Sessions, "Move this to desktop 4 and follow";
    DesktopSend5 = "desktop.send5", Sessions, "Move this to desktop 5 and follow";
    DesktopSend6 = "desktop.send6", Sessions, "Move this to desktop 6 and follow";
    DesktopSend7 = "desktop.send7", Sessions, "Move this to desktop 7 and follow";
    DesktopSend8 = "desktop.send8", Sessions, "Move this to desktop 8 and follow";
    DesktopSend9 = "desktop.send9", Sessions, "Move this to desktop 9 and follow";
    DesktopSendStay1 = "desktop.sendStay1", Sessions, "Move this to desktop 1";
    DesktopSendStay2 = "desktop.sendStay2", Sessions, "Move this to desktop 2";
    DesktopSendStay3 = "desktop.sendStay3", Sessions, "Move this to desktop 3";
    DesktopSendStay4 = "desktop.sendStay4", Sessions, "Move this to desktop 4";
    DesktopSendStay5 = "desktop.sendStay5", Sessions, "Move this to desktop 5";
    DesktopSendStay6 = "desktop.sendStay6", Sessions, "Move this to desktop 6";
    DesktopSendStay7 = "desktop.sendStay7", Sessions, "Move this to desktop 7";
    DesktopSendStay8 = "desktop.sendStay8", Sessions, "Move this to desktop 8";
    DesktopSendStay9 = "desktop.sendStay9", Sessions, "Move this to desktop 9";

    TerminalOpen = "terminal.open", Panes, "Focus utility terminal";
    TerminalSplitVertical = "terminal.splitVertical", Panes, "New shell beside";
    TerminalSplitHorizontal = "terminal.splitHorizontal", Panes, "New shell below";
    TerminalToggleZoom = "terminal.toggleZoom", Panes, "Zoom active pane";
    TerminalToggleMaximize = "terminal.toggleMaximize", Panes, "Focus active pane";
    TerminalClose = "terminal.close", Panes, "Close focused pane";
    TerminalFocusLeft = "terminal.focusLeft", Panes, "Move focus left";
    TerminalFocusRight = "terminal.focusRight", Panes, "Move focus right";
    TerminalFocusUp = "terminal.focusUp", Panes, "Move focus up";
    TerminalFocusDown = "terminal.focusDown", Panes, "Move focus down";
    ShelfFewerTiles = "shelf.fewerTiles", Panes, "Fewer, wider tiles";
    ShelfMoreTiles = "shelf.moreTiles", Panes, "More, narrower tiles";
    ShelfCarryLeft = "shelf.carryLeft", Panes, "Carry tile left along the row";
    ShelfCarryRight = "shelf.carryRight", Panes, "Carry tile right along the row";
    ShelfSendToShelf = "shelf.sendToShelf", Panes, "Send tile to the shelf";
    TilePopOut = "tile.popOut", Panes, "Pop out or bring back tile";
    TerminalFind = "terminal.find", Panes, "Find in terminal";

    MarkdownSendAnnotations = "markdown.sendAnnotations", Markdown, "Send annotations to session";
    TerminalSendAnnotations = "terminal.sendAnnotations", Markdown, "Send terminal annotations to session";

    DockAttention = "dock.attention", Review, "PRs drawer";
    SessionRefreshPrs = "session.refreshPRs", Review, "Refresh PRs";

    UiActionMenu = "ui.actionMenu", App, "Agent palette";
    UiCommandPalette = "ui.commandPalette", App, "Command palette";
    UiOpenSettings = "ui.openSettings", App, "Settings";
    UiShowShortcuts = "ui.showShortcuts", App, "Keyboard shortcuts";
    UiIncreaseFontSize = "ui.increaseFontSize", App, "Increase font size";
    UiDecreaseFontSize = "ui.decreaseFontSize", App, "Decrease font size";
    UiResetFontSize = "ui.resetFontSize", App, "Reset font size";
    FileOpen = "file.open", App, "Open a markdown file";
    NotebookOpenTile = "notebook.openTile", App, "Open Editor tile";
    NotebookOpenFullscreen = "notebook.openFullscreen", App, "Open Notebook fullscreen";
    BoardOpen = "board.open", App, "Open the garden";
    SessionsOpen = "sessions.open", App, "Open the sessions list";
    AppQuit = "app.quit", App, "Quit attn";
}

impl Action {
    /// Settings, Keyboard shortcuts and Quit can be rebound but never unbound: they are the
    /// way back to every other binding.
    pub fn is_protected(&self) -> bool {
        matches!(
            self,
            Action::UiOpenSettings | Action::UiShowShortcuts | Action::AppQuit
        )
    }

    /// Whether the action only acts on a terminal, so its keys do nothing without one open.
    pub fn requires_terminal(&self) -> bool {
        matches!(
            self,
            Action::TerminalOpen
                | Action::TerminalSplitVertical
                | Action::TerminalSplitHorizontal
                | Action::TerminalToggleZoom
                | Action::TerminalToggleMaximize
                | Action::TerminalClose
                | Action::TerminalFocusLeft
                | Action::TerminalFocusRight
                | Action::TerminalFocusUp
                | Action::TerminalFocusDown
                | Action::TerminalFind
                | Action::TerminalSendAnnotations
        )
    }

    /// The action that switches to the desktop on `slot`, for the slots a key reaches.
    pub fn desktop_select(slot: i64) -> Option<Action> {
        Action::from_id(&format!("desktop.select{slot}")).filter(|_| (1..=9).contains(&slot))
    }
}
