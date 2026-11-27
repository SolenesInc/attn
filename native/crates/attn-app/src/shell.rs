use attn_client::Desktop;
use attn_client::{Connection, Facts, Refusal};
use attn_ui::blocks::{Columns, DialogSize, Foot, Inline, at_foot};
use attn_ui::system::{Size, Type, words};
use attn_ui::{Dialog, Frame, Tokens};
use rune::{Chord, Cx, El, Tx, col, row};
use rune_kit::{Button, ButtonVariant};

use crate::action::Action;
use crate::{State, connection};

/// A sequence's second key must follow its leader within this long.
pub(crate) const LEADER_TIMEOUT: f64 = 0.6;
/// How long a notice stays.
const NOTICE_SECONDS: f64 = 6.0;
pub(crate) const TITLE_BAR_HEIGHT: f32 = 40.0;
/// The title bar's tools, which the notifications panel hangs under, starting past the traffic
/// lights.
pub(crate) const TITLE_BAR_LEFT: &str = "titlebar.left";
/// Where the close button sits, in points from the window's top left: its 14 point frame
/// centred in the title bar.
pub(crate) const TRAFFIC_LIGHTS: (f32, f32) = (14.0, 13.0);
/// The window points between the traffic lights' 74 point edge and the title bar's first
/// control, which starts at 86 at every text size.
const TITLE_BAR_GAP_PAST_LIGHTS: f32 = 12.0;

/// What the window shows in place of the home's choices while the home is away.
#[derive(Clone, Default)]
pub(crate) struct Away {
    pub(crate) current: String,
    /// The active tile of each desktop the person made one active on, by desktop id.
    pub(crate) active: std::collections::BTreeMap<String, String>,
}

/// What one window shows and where its keyboard is. Desktops, the active tile and the current
/// desktop are the daemon's; this is the window's own.
#[derive(Default)]
pub struct Window {
    pub view: View,
    pub sidebar_hidden: bool,
    /// The open drawers, oldest first: they lie side by side over the content, the oldest at the
    /// window's edge, and Escape closes the newest.
    pub drawers: Vec<Drawer>,
    pub layer: Option<Layer>,
    /// The open overview, which counts only while `Layer::Overview` is the layer.
    pub overview: Option<crate::desktop::Overview>,
    /// How large the area beside the sidebar last laid out, in points.
    pub(crate) main_size: (f32, f32),
    pub(crate) callout: Option<crate::callouts::Callout>,
    pub(crate) whats_new: crate::whats_new::Tour,
    pub(crate) diagnostic_report: Option<crate::diagnostic_report::Draft>,
    pub notice: Option<Notice>,
    pub(crate) leader: Option<Leader>,
    /// The first key of a vim sequence, waiting for its second.
    pub(crate) vim_prefix: Option<crate::vim::Pending>,
    /// The tile that takes keys while nothing else in the window has focus. It is apart from
    /// the active tile: another window, or the daemon, can change the active tile without
    /// taking this window's keyboard.
    pub keyboard: Option<Holder>,
    pub(crate) desk: crate::desktop::Desk,
    pub(crate) hints: crate::hints::Hints,
    pub(crate) words: crate::markdown::ids::Pointed,
    pub(crate) search: crate::palette::Search,
    pub agent_list_open: bool,
    /// The sidebar's Automations section shows finished off-screen runs too.
    pub(crate) automations_finished_open: bool,
    pub(crate) agent_filter: rune::TextEdit,
    /// The queue row the arrows reached, by key: the settle, priority and snooze keys act on
    /// it while the queue list holds the keyboard.
    pub(crate) queue_cursor: Option<crate::sidebar::AgentLine>,
    /// Wall-clock milliseconds as of the last change the window shows by time: turn ages and
    /// snooze deadlines read it, so it moves only when one of them would read differently.
    pub now_ms: i64,
    /// The queue's turns as of the last change, oldest first: where the next turn is found
    /// when the one on screen closes.
    pub(crate) queue_turns: Vec<String>,
    /// Home follows the next turn to open, as it does after the last turn closed.
    pub follow_next_turn: bool,
    /// The tiles the person made active while the home was away, and the desktop they last went
    /// to; the home's win again once it answers.
    pub(crate) away: Option<Away>,
    /// How many agents the window has asked the daemon to show, so only the latest refused show
    /// undoes what the window did ahead of it.
    pub(crate) shows_asked: u64,
    /// The home screen lists its snoozed agents, not only their count.
    pub home_snoozed_open: bool,
    /// The drain of each running auto-settle countdown whose agent is off screen, by agent.
    pub(crate) countdowns: std::collections::HashMap<String, crate::sidebar::Countdown>,
    pub(crate) snooze: Option<crate::sidebar::snooze::Menu>,
    pub(crate) harness_menu: Option<crate::harness_menu::Open>,
    pub(crate) bar_peek: crate::sidebar::bar_peek::Hover,
    pub(crate) queue_bar_room: crate::sidebar::crowding::Room,
    pub(crate) launching: crate::picker::Launching,
    pub(crate) closing_desktop: Option<crate::desktop::Closing>,
    /// Links opened before the current desktop arrived, in the order they came.
    pub(crate) links: Vec<crate::deep_link::Link>,
    pub(crate) width: crate::sidebar::width::Width,
    pub(crate) reorder: Option<crate::sidebar::reorder::Reorder>,
    pub(crate) session_menu: Option<crate::sidebar::session_menu::Menu>,
    pub(crate) rename: Option<crate::sidebar::rename::Rename>,
    pub(crate) context_cap: Option<crate::context_cap::ContextCap>,
    pub(crate) sender_pairing: crate::sender_pairing::SenderPairing,
    /// The popped-out leaf this window shows, when it is a tile window.
    pub(crate) tile: Option<crate::desktop::TileWindow>,
    /// The popped-out tile whose window handed this window the action it is running.
    pub(crate) forwarded: Option<Holder>,
    pub(crate) garden: crate::garden::Garden,
    pub(crate) asks: crate::asks::Asks,
    pub(crate) pull_requests: crate::home::pull_requests::PullRequests,
    pub(crate) automations: crate::automations::Automations,
    pub(crate) ledger: crate::ledger::Ledger,
    pub(crate) notifications: crate::notifications::Panel,
    pub(crate) update_run: crate::update_run::Banner,
    pub(crate) warnings: crate::warnings::Warnings,
    pub(crate) outpost_updates: crate::outpost_updates::Banner,
    pub(crate) shared_codex: crate::harness::codex::SharedCodex,
    pub(crate) settings: crate::settings::Dialog,
    pub(crate) crew: crate::crew::Panel,
    pub(crate) crew_rows: crate::sidebar::crew::Rows,
    pub(crate) chief: crate::chief::Chief,
    pub(crate) launch_desktop: crate::launch_desktop::Picker,
    pub(crate) shortcut_editor: crate::shortcut_editor::Editor,
    pub(crate) profile_switcher: crate::profile_switcher::Switcher,
    /// The migration failure screen's details went to the clipboard.
    pub(crate) migration_details_copied: bool,
}

/// What the window's main area shows: home, or the current desktop's tiles.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum View {
    #[default]
    Home,
    Desktop,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Drawer {
    Automations,
    Garden,
}

impl Drawer {
    /// How wide the drawer lies in a window `window` points wide: its share of the window,
    /// clamped between its least and greatest width.
    pub(crate) fn width(self, window: f32) -> f32 {
        let (min, share, max) = match self {
            Drawer::Automations => (420.0, 0.42, 640.0),
            Drawer::Garden => (380.0, 0.34, 560.0),
        };
        (window * share).clamp(min, max)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Layer {
    Palette(Palette),
    /// The New Session dialog; `Window::launching` holds it.
    NewSession,
    Settings,
    Shortcuts,
    /// The editor that rebinds shortcuts; `Window::shortcut_editor` holds it.
    ShortcutEditor,
    /// The desktop overview.
    Overview,
    /// The ledger of sessions; `Window::ledger` holds it.
    Ledger,
    /// The notifications panel; `Window::notifications` holds it.
    Notifications,
    /// The profile switcher under the title bar's pill; `Window::profile_switcher` holds it.
    ProfileSwitcher,
    /// The confirmation before closing a desktop that holds tiles; `Window::closing_desktop`
    /// holds it.
    CloseDesktop,
    /// The tour of what this release brings; `Window::whats_new` holds it.
    WhatsNew,
    /// The dialog that saves a diagnostic report; `Window::diagnostic_report` holds it.
    DiagnosticReport,
    /// The notebook over the whole window; `Notebook::fullscreen` holds it.
    Notebook,
    /// Manage crew; `Window::crew` holds it.
    Crew,
    /// The asks sheet; `Window::asks` holds it.
    Asks,
    /// The confirm before restarting the shared Codex now or reloading its old tiles;
    /// `Window::shared_codex` holds it.
    SharedCodex,
    /// A banner's details: everything it says, whole, for as long as the banner says the
    /// message whose identity it holds.
    BannerDetails(Band, u64),
}

/// The banners over the window's work.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Band {
    HomeMove,
    UpdateRun,
    OutpostUpdates,
    Connection,
    SharedCodex,
    Warnings,
}

pub(crate) fn opens_details(band: Band) -> impl Fn(&mut State, &mut Tx) + 'static {
    move |state: &mut State, _: &mut Tx| {
        if let Some(message) = message(state, band) {
            state.window.layer = Some(Layer::BannerDetails(band, message));
        }
    }
}

pub(crate) fn close_details(state: &mut State) {
    if matches!(state.window.layer, Some(Layer::BannerDetails(..))) {
        state.window.layer = None;
    }
}

/// The message a band's banner shows, by its identity.
fn message(state: &State, band: Band) -> Option<u64> {
    use std::hash::{Hash, Hasher};
    let identity = match band {
        Band::HomeMove => crate::settings::machines::identity(state),
        Band::UpdateRun => crate::update_run::identity(state),
        Band::OutpostUpdates => crate::outpost_updates::identity(state),
        Band::SharedCodex => crate::harness::codex::identity(state),
        Band::Warnings => crate::warnings::identity(state),
        Band::Connection => crate::connection::identity(state),
    }?;
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    identity.hash(&mut hasher);
    Some(hasher.finish())
}

/// Closes a banner's details once its banner says another message or none, so a new message
/// starts closed.
pub(crate) fn follow_details(state: &mut State) -> bool {
    let Some(Layer::BannerDetails(band, shown)) = state.window.layer else {
        return false;
    };
    let moved_on = message(state, band) != Some(shown);
    if moved_on {
        state.window.layer = None;
    }
    moved_on
}

fn banner_details(state: &State, band: Band, shown: u64, t: &Tokens, layers: &mut Vec<El>) {
    if message(state, band) != Some(shown) {
        return;
    }
    let banner = match band {
        Band::HomeMove => crate::settings::machines::said(state),
        Band::UpdateRun => crate::update_run::said(state),
        Band::OutpostUpdates => crate::outpost_updates::said(state),
        Band::SharedCodex => crate::harness::codex::said(state),
        Band::Warnings => crate::warnings::said(state),
        Band::Connection => crate::connection::said(state),
    };
    if let Some(banner) = banner {
        banner.build_details(
            t,
            |state: &mut State, _: &mut Tx| close_details(state),
            layers,
        );
    }
}

impl Layer {
    /// Whether the layer keeps `action`'s keys from acting while it is open. A layer keeps its own
    /// keys: the agents key toggles the palette and its turn keys act on its highlighted agent; the
    /// keys that open the markdown opener, the overview and the profile switcher close them; the
    /// notebook's file key finds a note. The overview also keeps every tile's keys but the walk's,
    /// which move its highlight.
    pub(crate) fn holds(self, action: &Action) -> bool {
        let captures_navigation = matches!(
            self,
            Layer::Palette(_)
                | Layer::ShortcutEditor
                | Layer::Overview
                | Layer::Ledger
                | Layer::ProfileSwitcher
                | Layer::WhatsNew
                | Layer::Notebook
                | Layer::Crew
                | Layer::Asks
        );
        let own = matches!(
            (self, action),
            (
                Layer::Palette(Palette::Search),
                Action::SidebarAgentList
                    | Action::SessionSettle
                    | Action::SessionPriority
                    | Action::SessionSnooze
            ) | (
                Layer::Palette(Palette::Files) | Layer::Notebook,
                Action::FileOpen
            ) | (Layer::Overview, Action::DesktopOverview)
                | (Layer::ProfileSwitcher, Action::ProfileSwitch)
                | (Layer::Asks, Action::DockAsks)
                | (Layer::Asks | Layer::Crew, Action::TerminalLabelIds)
        );
        let tiles = self == Layer::Overview
            && action.category() == crate::action::Category::Panes
            && action.walks_toward().is_none();
        tiles || (captures_navigation && action.yields_to_overlays() && !own)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Palette {
    /// Agents, or commands behind a leading `>`.
    Search,
    Files,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Holder {
    pub desktop: String,
    pub pane: String,
}

#[derive(Clone, Debug)]
pub struct Notice {
    pub text: String,
    until: f64,
    /// The pointer rests on it, which keeps it until the pointer leaves.
    held: bool,
    offer: Option<Offer>,
}

/// What a notice offers to do about what it says, as Undo after a sort, and whether it did.
#[derive(Clone, Debug)]
struct Offer {
    label: &'static str,
    action: Action,
    done: bool,
}

#[derive(Clone, Debug)]
pub(crate) struct Leader {
    pub(crate) chord: Chord,
    pub(crate) sequences: Vec<(Action, Chord)>,
    pub(crate) until: f64,
}

const PALETTE_FIELD: &str = "palette.field";

/// Escape closes the garden when it holds the window, else the newest drawer, whatever has
/// focus.
pub(crate) fn escape_drawers(state: &mut State, tx: &mut Tx) -> bool {
    if crate::garden::mode(state) == crate::garden::Mode::Full {
        crate::garden::close(state);
        return true;
    }
    match state.window.newest_drawer() {
        Some(Drawer::Garden) => crate::garden::close(state),
        Some(Drawer::Automations) => crate::automations::close(state, tx),
        None => return false,
    }
    true
}

impl Window {
    pub(crate) fn go_home(&mut self) {
        self.close_overview();
        self.view = View::Home;
        self.desk.header_pop = Default::default();
        self.follow_next_turn = false;
        self.desk.awaited = None;
    }

    /// Home after the last turn closed, ready to open the next one that opens.
    pub(crate) fn go_home_following(&mut self) {
        self.view = View::Home;
        self.desk.header_pop = Default::default();
        self.follow_next_turn = true;
        self.desk.awaited = None;
    }

    /// Closes the open overview; whether there was one.
    pub(crate) fn close_overview(&mut self) -> bool {
        let open = self.layer == Some(Layer::Overview);
        if open {
            self.layer = None;
            self.overview = None;
        }
        open
    }

    /// Shows the current desktop, which closes the overview. Coming from another view lands at
    /// once.
    pub(crate) fn show_desktop(&mut self, tx: &mut Tx) {
        if self.close_overview() {
            tx.blur();
        }
        if self.view != View::Desktop {
            tx.instant();
        }
        self.view = View::Desktop;
    }

    /// The window's tiles show with nothing over them, so keys may act on them and type into
    /// them: a tile window's tile, or the current desktop outside home and any layer.
    pub(crate) fn tiles_in_front(&self) -> bool {
        self.tile.is_some() || (self.view == View::Desktop && self.layer.is_none())
    }

    /// The terminal that keys type into: the keyboard holder's, while its tiles are in front.
    pub(crate) fn typing_terminal(&self, facts: &Facts) -> Option<String> {
        self.tiles_in_front()
            .then(|| self.keyboard_terminal(facts))
            .flatten()
    }

    /// The desktop and the tile this window's keys, commands and finders act on: a tile
    /// window's own tile, else the popped-out tile it acts for, else the current desktop's
    /// acting leaf.
    pub(crate) fn acting<'f>(&self, facts: &'f Facts) -> Option<(&'f Desktop, String)> {
        if let Some(tile) = &self.tile {
            let desktop = facts.desktops.iter().find(|d| d.id == tile.desktop)?;
            return Some((desktop, tile.leaf.clone()));
        }
        if let Some(tile) = self.acting_for() {
            let desktop = facts.desktops.iter().find(|d| d.id == tile.desktop)?;
            return Some((desktop, tile.pane.clone()));
        }
        let desktop = facts.current_desktop()?;
        let leaf = self.desk.acting_leaf(desktop);
        (!leaf.is_empty()).then_some((desktop, leaf))
    }

    /// The agent the window shows: the agent of the tile it acts on.
    pub(crate) fn on_screen(&self, facts: &Facts) -> Option<String> {
        if self.view != View::Desktop && self.acting_for().is_none() {
            return None;
        }
        let (desktop, leaf) = self.acting(facts)?;
        desktop
            .panes
            .iter()
            .find(|p| p.pane_id == leaf)
            .map(|p| p.session_id.clone())
            .filter(|id| !id.is_empty())
    }

    /// The popped-out tile this main window acts for: the one whose window handed it the
    /// running action, or the one whose window opened the palette that is open.
    fn acting_for(&self) -> Option<&Holder> {
        let palette = (self.layer == Some(Layer::Palette(Palette::Search)))
            .then_some(self.search.tile.as_ref())
            .flatten();
        self.forwarded.as_ref().or(palette)
    }

    pub(crate) fn shows(&self, drawer: Drawer) -> bool {
        self.drawers.contains(&drawer)
    }

    pub(crate) fn newest_drawer(&self) -> Option<Drawer> {
        self.drawers.last().copied()
    }

    pub(crate) fn open_drawer(&mut self, drawer: Drawer) {
        if !self.shows(drawer) {
            self.drawers.push(drawer);
        }
    }

    pub(crate) fn close_drawer(&mut self, drawer: Drawer) {
        self.drawers.retain(|d| *d != drawer);
    }

    pub(crate) fn toggle_layer(&mut self, layer: Layer, tx: &mut Tx) {
        if self.layer == Some(layer) {
            self.layer = None;
            crate::picker::refocus_inline_window(self, tx);
            return;
        }
        self.layer = Some(layer);
        if let Layer::Palette(_) = layer {
            self.search.query = rune::TextEdit::default();
            tx.focus(PALETTE_FIELD, false);
        }
    }

    pub(crate) fn notify(&mut self, text: String, tx: &mut Tx) {
        self.show_notice(text, None, tx);
    }

    /// A notice whose button runs `action`, once.
    pub(crate) fn notify_offering(
        &mut self,
        text: String,
        label: &'static str,
        action: Action,
        tx: &mut Tx,
    ) {
        let offer = Offer {
            label,
            action,
            done: false,
        };
        self.show_notice(text, Some(offer), tx);
    }

    fn show_notice(&mut self, text: String, offer: Option<Offer>, tx: &mut Tx) {
        let until = tx.clock() + NOTICE_SECONDS;
        self.notice = Some(Notice {
            text,
            until,
            held: false,
            offer,
        });
        tx.wake_at(until);
    }

    /// Keeps the keyboard on a tile of the current desktop. It moves with the current desktop
    /// and when its tile goes, to that desktop's active tile, and stays put when only the
    /// active tile changes.
    pub(crate) fn follow(&mut self, facts: &Facts) {
        self.desk.follow(facts);
        if self.tile.is_some() {
            return;
        }
        let Some(desktop) = facts.current_desktop() else {
            self.keyboard = None;
            return;
        };
        if let Some(session) = self.desk.split.clone()
            && let Some((on, pane)) = facts.desktops.iter().find_map(|d| {
                let pane = d.panes.iter().find(|p| p.session_id == session)?;
                Some((d.id.clone(), pane.pane_id.clone()))
            })
        {
            self.desk.split = None;
            if on == desktop.id {
                self.desk.pick(&on, &pane);
                self.keyboard = Some(Holder { desktop: on, pane });
                return;
            }
        }
        let holds = |pane: &str| {
            desktop.panes.iter().any(|p| p.pane_id == pane)
                || desktop
                    .leaves()
                    .iter()
                    .any(|leaf| leaf.id == pane && leaf.tile().is_some())
        };
        let kept = self
            .keyboard
            .as_ref()
            .is_some_and(|holder| holder.desktop == desktop.id && holds(&holder.pane));
        if kept {
            return;
        }
        self.keyboard = holds(&desktop.active_pane_id).then(|| Holder {
            desktop: desktop.id.clone(),
            pane: desktop.active_pane_id.clone(),
        });
    }

    /// The terminal the keyboard holder shows, by its runtime id.
    pub(crate) fn keyboard_terminal(&self, facts: &Facts) -> Option<String> {
        let holder = self.keyboard.as_ref()?;
        let desktop = facts.desktops.iter().find(|d| d.id == holder.desktop)?;
        let pane = desktop.panes.iter().find(|p| p.pane_id == holder.pane)?;
        (!pane.runtime_id.is_empty()).then(|| pane.runtime_id.clone())
    }
}

/// Lets a notice, a sequence's leader and the desktops' moments lapse when their time is up.
pub(crate) fn expire(state: &mut State, _: f64, tx: &mut Tx) -> bool {
    let switched = crate::desktop::expire(state, tx);
    let now = tx.clock();
    let window = &mut state.window;
    let notice = window
        .notice
        .take_if(|n| !n.held && now >= n.until)
        .is_some();
    let leader = window.leader.take_if(|l| now >= l.until).is_some();
    let vim = window.vim_prefix.take_if(|p| now >= p.until).is_some();
    let label = window
        .desk
        .width_label
        .take_if(|l| now >= l.until)
        .is_some();
    let sidebar_label = window
        .width
        .label_until
        .take_if(|until| now >= *until)
        .is_some();
    switched || notice || leader || vim || label || sidebar_label
}

pub(crate) fn view(state: &State, cx: &mut Cx<State>) -> El {
    cx.controls_keep_focus();
    let t = crate::colors(&state.facts.settings, cx.system_dark()).ui;
    cx.zoom(crate::settings::text_scale(&state.facts.settings));
    let connection = &state.facts.connection;
    let desk = connection::shows_desk(&state.facts);
    let body = match connection {
        Connection::Connected | Connection::Reconnecting { .. } => shell(state, &t, cx.env().scale),
        Connection::HomeUnreachable(_) if desk => shell(state, &t, cx.env().scale),
        Connection::Connecting => centered(&t, connection::connecting(&t)),
        Connection::Refused(Refusal::MigrationFailed(marker)) => {
            centered(&t, crate::migration_failure::screen(state, marker, &t))
        }
        Connection::Refused(refusal) => centered(&t, connection::refused(&t, refusal)),
        Connection::HomeUnreachable(away) => centered(
            &t,
            connection::home_unreachable(
                &t,
                away,
                crate::update_run::current(state),
                state.window.notice.as_ref().map(|n| n.text.as_str()),
                crate::settings::machines::undoable_move(state),
                crate::settings::machines::rollback_failure(state),
            ),
        ),
    };
    let banner = connection::banner(state, &t, cx);
    let bands = desk;
    let warnings = bands
        .then(|| crate::warnings::banner(state, &t, cx))
        .flatten();
    let update_run = bands
        .then(|| crate::update_run::banner(state, &t, cx))
        .flatten();
    let home_move = bands
        .then(|| crate::settings::machines::banner(state, &t, cx))
        .flatten();
    let outpost_updates = bands
        .then(|| crate::outpost_updates::banner(state, &t, cx))
        .flatten();
    let shared_codex = bands
        .then(|| crate::harness::codex::banner(state, &t, cx))
        .flatten();
    let root = col()
        .key("window")
        .w_full()
        .h_full()
        .fill(t.bg_app)
        .on_window_focus(crate::terminal::window_focused)
        .on_paste_content(crate::keys::on_paste)
        .child(title_bar(state, cx, &t))
        .children(
            [
                banner,
                home_move,
                warnings,
                outpost_updates,
                update_run,
                shared_codex,
            ]
            .into_iter()
            .flatten(),
        )
        .children(
            bands
                .then(|| crate::desktop::header_pop::words_anchor(state))
                .flatten(),
        )
        .child(body.min_h(WORK_AREA_FLOOR));
    if !bands {
        return root;
    }
    crate::markdown::with_image_preview(root, layers(state, &t), state, cx.is_reduced_motion(), &t)
        .children(crate::hints::words_layers(state, &t))
        .children(crate::markdown::annotating::layers(state, cx, &t))
        .children(crate::markdown::sending::layers(state, cx, &t))
        .children(crate::callouts::layers(state, cx, &t))
}

/// The least height the bands leave the work under them: a tile's header, 41, and four
/// terminal rows of 21 at 14 points, an agent's composer box and the line above it. The bands
/// never reach it closed: their worst real case at the smallest window leaves 237.
const WORK_AREA_FLOOR: f32 = 125.0;

/// The band the window drags by, over which the window's own controls sit; what it holds
/// comes with the queue bar and the title bar controls.
fn title_bar(state: &State, cx: &Cx<State>, t: &Tokens) -> El {
    let connected = connection::shows_desktops(&state.facts.connection);
    let lights_end = cx.chrome_insets().leading;
    let lights = (lights_end > 0.0).then(|| {
        lights_end + TITLE_BAR_GAP_PAST_LIGHTS / crate::settings::text_scale(&state.facts.settings)
    });
    let start = lights.unwrap_or(attn_ui::blocks::WindowBar::inset());
    let beside_sidebar =
        !state.window.sidebar_hidden && !crate::desktop::shows_an_agent_alone(state);
    let left = attn_ui::blocks::Inline::joined()
        .key(TITLE_BAR_LEFT)
        .min_w(if beside_sidebar {
            (crate::sidebar::width::of(state) - start).max(0.0)
        } else {
            0.0
        })
        .children(
            connected
                .then(|| crate::title_bar::title_tools(state, t))
                .into_iter()
                .flatten(),
        );
    let (middle, given_up) = if connected {
        crate::sidebar::title_middle(state, crate::desktop::shelf_slot(state, t), t)
    } else {
        (row(), crate::sidebar::crowding::GivenUp::Nothing)
    };
    let middle = row()
        .key("titlebar.middle")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .items_center()
        .on_measure(crate::sidebar::crowding::middle_laid_out)
        .child(middle);
    let short_profile = given_up >= crate::sidebar::crowding::GivenUp::ProfileName;
    let right = attn_ui::blocks::Inline::joined()
        .key("titlebar.right")
        .children(
            connected
                .then(|| crate::title_bar::title_panels(state, t))
                .into_iter()
                .flatten(),
        )
        .children(connected.then(|| attn_ui::blocks::WindowBar::rule(t)))
        .children(instance_marker(state, t))
        .children(connected.then(|| crate::profile_switcher::pill(state, short_profile, t)));
    let parts = [left, middle, right];
    let covered = connected && title_bar_covered(state);
    attn_ui::blocks::WindowBar::across(lights, TITLE_BAR_HEIGHT, t)
        .key("titlebar")
        .role("banner")
        .label("Title bar")
        .window_drag()
        .on_measure(crate::sidebar::crowding::bar_laid_out)
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.desk.window_w == Some(w) {
                tx.live_only();
                return;
            }
            state.window.desk.window_w = Some(w);
        })
        .children(parts.into_iter().map(|part| {
            if covered {
                part.layer_opacity(COVERED_OPACITY)
            } else {
                part
            }
        }))
}

/// Whether something holds the window so that the title bar's controls fade: every layer but the
/// notifications panel and the overview, and the garden over the whole window.
fn title_bar_covered(state: &State) -> bool {
    !matches!(
        state.window.layer,
        None | Some(Layer::Notifications | Layer::Overview)
    ) || crate::garden::mode(state) == crate::garden::Mode::Full
}

const COVERED_OPACITY: f32 = 0.45;

/// The name of a non-production instance, so its window never passes for production's.
fn instance_marker(state: &State, t: &Tokens) -> Option<El> {
    let name = state.instance.as_str();
    (name != "default" && !name.is_empty()).then(|| {
        attn_ui::system::words(name.to_owned(), attn_ui::system::Type::Mono, t.warning)
            .key("titlebar.instance")
            .role("note")
            .label(name.to_owned())
            .description(format!("Instance: {name}"))
            .single_line()
            .truncate()
            .max_w(INSTANCE_MARKER_MAX_W)
    })
}

/// The widest an instance's name stands before it truncates: about thirteen characters of
/// `Mono`, so a long name never pushes the title bar's tools aside.
const INSTANCE_MARKER_MAX_W: f32 = 96.0;

/// `control` in a slot keyed `key` as tall as the title bar, for a popover that hangs a set gap
/// under the title bar.
pub(crate) fn title_bar_slot(key: &str, control: El) -> El {
    col()
        .key(key)
        .h(TITLE_BAR_HEIGHT)
        .justify_center()
        .child(control)
}

/// A surface that holds the window under the title bar, as the ledger, the notebook and the
/// garden do: the app background over everything below the title bar, and `panel` set in from
/// its edges.
pub(crate) fn under_title_bar(key: &str, panel: El, t: &Tokens) -> El {
    attn_ui::blocks::Surface::under_bar(panel, t).key(key).pin(
        Some(0.0),
        Some(TITLE_BAR_HEIGHT),
        Some(0.0),
        Some(0.0),
    )
}

fn centered(t: &Tokens, content: El) -> El {
    col()
        .w_full()
        .grow(1.0)
        .center()
        .fill(t.bg_app)
        .child(content)
}

fn shell(state: &State, t: &Tokens, scale: f32) -> El {
    row()
        .key("regions")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.width.window == w {
                tx.live_only();
                return;
            }
            state.window.width.window = w;
            state.notebook.window_w = w;
            state.notebook.revision += 1;
        })
        .children(crate::sidebar::view(state, t))
        .children(main_area(state, t, scale))
}

/// What shows beside the sidebar: Manage crew while it is open, else home or the desktop with
/// its open drawers.
fn main_area(state: &State, t: &Tokens, scale: f32) -> Vec<El> {
    if crate::crew::is_open(state) {
        return vec![crate::crew::page(state, t)];
    }
    let shown = match state.window.view {
        View::Home => row()
            .key("home.area")
            .grow(1.0)
            .min_w(0.0)
            .h_full()
            .child(crate::home::view(state, t)),
        View::Desktop => crate::desktop::area(state, t, scale),
    };
    let main = shown
        .on_layout_tx(|state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if state.window.main_size == (w, h) {
                tx.live_only();
                return;
            }
            state.window.main_size = (w, h);
        })
        .children(crate::desktop::overview(state, t));
    std::iter::once(main).chain(dock(state, t)).collect()
}

/// The open drawers over the content, below the title bar, nothing behind them moving: the
/// oldest against the window's right edge and each newer one to its left.
fn dock(state: &State, t: &Tokens) -> Vec<El> {
    let mut right = 0.0;
    docked(state)
        .map(|(drawer, width)| {
            let content = match drawer {
                Drawer::Garden => crate::garden::panel(state, t),
                Drawer::Automations => crate::automations::drawer(state, t),
            };
            let at = right;
            right += width;
            drawn_as_dock_panel(content, t)
                .pin(None, Some(0.0), Some(at), Some(0.0))
                .w(width)
        })
        .collect()
}

/// The drawers the dock lays out, oldest first, each with its width: all the open ones but
/// a garden that holds the window.
fn docked(state: &State) -> impl Iterator<Item = (Drawer, f32)> + '_ {
    let window = &state.window;
    window
        .drawers
        .iter()
        .copied()
        .filter(|&d| {
            !(d == Drawer::Garden && crate::garden::mode(state) == crate::garden::Mode::Full)
        })
        .map(|d| (d, d.width(window.width.window)))
}

/// How much of the window's right side the open drawers cover.
pub(crate) fn dock_width(state: &State) -> f32 {
    docked(state).map(|(_, width)| width).sum()
}

/// The surface every dock panel has: the panel color under a
/// faint sheen, a rule on its left, and a shadow cast over the content.
fn drawn_as_dock_panel(content: El, t: &Tokens) -> El {
    content
        .fill(t.bg_panel)
        .gradient(t.highlight.alpha(0.03), t.highlight.alpha(0.01))
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border)
        .box_shadow(rune::Shadow {
            x: -24.0,
            blur: 60.0,
            color: t.shadow.alpha(0.4),
            ..rune::Shadow::default()
        })
}

impl Window {
    pub(crate) fn hold_keyboard(&mut self, desktop: &str, pane: &str) {
        self.keyboard = Some(Holder {
            desktop: desktop.into(),
            pane: pane.into(),
        });
        self.queue_cursor = None;
    }
}

fn layers(state: &State, t: &Tokens) -> Vec<El> {
    let window = &state.window;
    let mut layers = over_tiles(state, t);
    if let Some(leader) = &window.leader {
        layers.push(floating_status(
            "leader",
            format!(
                "{} then…",
                state.keymap.label(crate::Binding::Chord(leader.chord))
            ),
            t,
        ));
    }
    if let Some(notice) = &window.notice {
        layers.push(match &notice.offer {
            None => told(notice, t),
            Some(offer) => offering(notice, offer, t),
        });
    }
    layers
}

/// Whether the window draws something over its tiles, under which a browser tile's page hides:
/// a dialog, the palette, a menu, a popover, a callout or a drag. A notice and a sequence's
/// hint sit at the window's foot and leave the page showing.
pub(crate) fn covers_tiles(state: &State) -> bool {
    state.window.callout.is_some()
        || state.window.layer == Some(Layer::Overview)
        || !over_tiles(state, &crate::color_scheme(&state.facts.settings).dark.ui).is_empty()
}

fn over_tiles(state: &State, t: &Tokens) -> Vec<El> {
    let window = &state.window;
    let mut layers: Vec<El> = crate::garden::frame_layer(state, t).into_iter().collect();
    crate::garden::overlay(state, t, &mut layers);
    match window.layer {
        Some(Layer::Palette(Palette::Search)) => layers.push(crate::palette::view(state, t)),
        Some(Layer::Palette(Palette::Files)) => layers.push(crate::opener::view(state, t)),
        Some(Layer::NewSession) => layers.extend(crate::picker::dialog(state, t)),
        Some(Layer::Ledger) => {
            layers.push(crate::ledger::view(state, t));
            layers.extend(crate::ledger::row_menu(state, t));
        }
        Some(Layer::Notifications) => layers.push(crate::notifications::panel(state, t)),
        Some(Layer::CloseDesktop) => layers.extend(crate::desktop::close_confirmation(state, t)),
        Some(Layer::Settings) => crate::settings::dialog(state, t, &mut layers),
        Some(Layer::Shortcuts) => shortcuts(state, t, &mut layers),
        Some(Layer::ShortcutEditor) => crate::shortcut_editor::view(state, t, &mut layers),
        Some(Layer::ProfileSwitcher) => layers.extend(crate::profile_switcher::view(state, t)),
        Some(Layer::WhatsNew) => crate::whats_new::dialog(state, t, &mut layers),
        Some(Layer::DiagnosticReport) => crate::diagnostic_report::dialog(state, t, &mut layers),
        Some(Layer::Notebook) => layers.push(crate::notebook::fullscreen(state, t)),
        Some(Layer::Crew) => crate::crew::overlays(state, t, &mut layers),
        Some(Layer::SharedCodex) => crate::harness::codex::confirm(state, t, &mut layers),
        Some(Layer::Asks) => layers.push(crate::asks::sheet(state, t)),
        Some(Layer::BannerDetails(band, shown)) => {
            banner_details(state, band, shown, t, &mut layers)
        }
        Some(Layer::Overview) | None => {}
    }
    layers.extend(crate::home::pull_requests::merge_confirmation(state, t));
    crate::notebook::root_picker_overlay(state, t, &mut layers);
    crate::automations::form::overlay(state, t, &mut layers);
    layers.extend(crate::desktop::width_control_layer(state, t));
    layers.extend(crate::desktop::peek(state, t));
    if !crate::desktop::header_pop::is_in_sheet(state) {
        layers.extend(crate::desktop::header_popover(state, t));
    }
    layers.extend(crate::sidebar::bar_peek::view(state, t));
    layers.extend(crate::sidebar::snooze::view(state, t));
    layers.extend(crate::harness_menu::view(state, t));
    layers.extend(crate::sidebar::session_menu::view(state, t));
    layers.extend(crate::sidebar::crew::menu_view(state, t));
    crate::chief::setup::overlay(state, t, &mut layers);
    layers.extend(crate::sidebar::rename::view(state, t));
    crate::context_cap::view(state, t, &mut layers);
    layers.extend(crate::desktop::width_label(state, t));
    layers.extend(crate::desktop::drag_overlay(state, t));
    layers.extend(crate::picker::worktree_setup(state, t));
    crate::sender_pairing::view(state, t, &mut layers);
    layers
}

/// Closes what reads words over the tiles, so what an id in them opens shows.
pub(crate) fn uncover_tiles(state: &mut State, tx: &mut Tx) {
    if crate::asks::is_open(state) {
        crate::asks::close(state, tx);
    }
    if crate::crew::is_open(state) {
        crate::crew::close(state, tx);
    }
    if crate::garden::mode(state) == crate::garden::Mode::Full {
        crate::garden::close(state);
    }
}

fn close_layer(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

fn shortcuts(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let [left, right] =
        crate::cheat_sheet::columns(&state.keymap, crate::vim::enabled(&state.facts.settings), t);
    Dialog::new("shortcuts", "Keyboard Shortcuts")
        .size(DialogSize::Sheet)
        .body_key("shortcuts.list")
        .aside(
            attn_ui::system::Button::new("Edit shortcuts")
                .size(Size::Compact)
                .on_press_tx(crate::shortcut_editor::open)
                .build(t)
                .key("shortcuts.edit"),
        )
        .on_close_tx(close_layer)
        .push(
            Columns::new("shortcuts.columns")
                .column(left)
                .column(right)
                .build(),
        )
        .build(t, layers);
}

/// A notice that only tells: a card at the window's bottom right.
fn told(notice: &Notice, t: &Tokens) -> El {
    let said = words(notice.text.clone(), Type::Body, t.text_primary)
        .key("notice.text")
        .role("status")
        .label(notice.text.clone())
        .wrap()
        .grow(1.0)
        .min_w(0.0);
    at_foot(
        "notice",
        toast_card(toast_glyph("!", t.error, t), said, t),
        Foot::End,
    )
    .inert()
}

/// A notice with its offer: one button that runs the offer
/// and then says it is done. The pointer resting on it keeps it.
fn offering(notice: &Notice, offer: &Offer, t: &Tokens) -> El {
    let said = if offer.done { "✓ Done" } else { offer.label };
    let face = notice.text.clone();
    let (ink, said_ink) = (
        t.text_primary,
        if offer.done { t.text_muted } else { t.brand },
    );
    let button = Button::new(format!("{} {said}", notice.text))
        .variant(ButtonVariant::Ghost)
        .disabled(offer.done)
        .on_press_tx(|state: &mut State, tx: &mut Tx| take_offer(state, tx))
        .label_with(move |_, _| {
            Inline::joined()
                .w_full()
                .child(words(face, Type::Body, ink).grow(1.0).min_w(0.0))
                .child(words(said, Type::Small, said_ink))
        })
        .build(attn_ui::kit(t))
        .key("notice.offer")
        .grow(1.0)
        .min_w(0.0);
    at_foot(
        "notice",
        toast_card(toast_glyph("✓", t.text_secondary, t), button, t).on_hover_change(
            |state: &mut State, inside, tx: &mut Tx| hold_notice(state, inside, tx),
        ),
        Foot::End,
    )
}

fn toast_glyph(glyph: &str, ink: rune::Color, t: &Tokens) -> El {
    attn_ui::system::chip_edge(words(glyph.to_owned(), Type::Tag, ink), t).a11y_hidden()
}

/// The toast card.
fn toast_card(glyph: El, body: El, t: &Tokens) -> El {
    Frame::panel(t)
        .draw_holding(Inline::joined().w_full().child(glyph).child(body))
        .key("notice.card")
        .w(TOAST_W)
}

const TOAST_W: f32 = 360.0;

fn hold_notice(state: &mut State, inside: bool, tx: &mut Tx) {
    let Some(notice) = &mut state.window.notice else {
        return;
    };
    notice.held = inside;
    if !inside {
        notice.until = tx.clock() + NOTICE_SECONDS;
        tx.wake_at(notice.until);
    }
}

fn take_offer(state: &mut State, tx: &mut Tx) {
    let Some(offer) = state.window.notice.as_mut().and_then(|n| n.offer.as_mut()) else {
        return;
    };
    if offer.done {
        return;
    }
    offer.done = true;
    let action = offer.action.clone();
    crate::dispatch::dispatch(state, action, tx);
}

fn floating_status(key: &str, message: String, t: &Tokens) -> El {
    let status = words(message.clone(), Type::Body, t.text_primary)
        .key(format!("{key}.text"))
        .role("status")
        .label(message);
    at_foot(key, Frame::panel(t).draw_holding(status), Foot::Centre).inert()
}
