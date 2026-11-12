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
        /// Everything a person can ask the app to do by key, menu or palette. The id is the
        /// shortcut id the daemon's `keybindings_config` setting keys rebinds by, shared with
        /// the Tauri app.
        #[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
        pub enum Action {
            $($variant,)*
        }

        impl Action {
            pub const ALL: &[Action] = &[$(Action::$variant,)*];

            pub fn id(self) -> &'static str {
                match self {
                    $(Action::$variant => $id,)*
                }
            }

            pub fn label(self) -> &'static str {
                match self {
                    $(Action::$variant => $label,)*
                }
            }

            pub fn category(self) -> Category {
                match self {
                    $(Action::$variant => Category::$category,)*
                }
            }

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
    TerminalCollapse = "terminal.collapse", Panes, "Collapse utility terminal";
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
    pub fn is_protected(self) -> bool {
        matches!(
            self,
            Action::UiOpenSettings | Action::UiShowShortcuts | Action::AppQuit
        )
    }
}
