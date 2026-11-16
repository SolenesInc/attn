use attn_client::{Connection, Facts, Refusal};
use attn_protocol::Desktop;
use attn_ui::{Appearance, Tokens};
use rune::tok::{space, type_};
use rune::{Chord, Cx, El, Tx, col, el, row, text};
use rune_kit::popover::raised;
use rune_kit::{Button, ButtonVariant, Dialog};

use crate::action::{Action, Category};
use crate::keymap::Keymap;
use crate::{State, connection};

/// A sequence's second key must follow its leader within this long, as in the Tauri app.
pub(crate) const LEADER_TIMEOUT: f64 = 0.6;
/// How long a notice stays, as the Tauri app's toast does.
const NOTICE_SECONDS: f64 = 6.0;
pub(crate) const TITLE_BAR_HEIGHT: f32 = 32.0;

/// What one window shows and where its keyboard is. Desktops, the active tile and the current
/// desktop are the daemon's; this is the window's own.
#[derive(Default)]
pub struct Window {
    pub view: View,
    pub sidebar_hidden: bool,
    /// The open drawers, oldest first: they stand side by side, and Escape closes the newest.
    pub drawers: Vec<Drawer>,
    pub layer: Option<Layer>,
    pub(crate) callout: Option<crate::callouts::Callout>,
    pub(crate) whats_new: crate::whats_new::Tour,
    pub(crate) diagnostic_report: Option<crate::diagnostic_report::Draft>,
    pub notice: Option<Notice>,
    pub(crate) leader: Option<Leader>,
    /// The tile that takes keys while nothing else in the window has focus. It is apart from
    /// the active tile: another window, or the daemon, can change the active tile without
    /// taking this window's keyboard.
    pub keyboard: Option<Holder>,
    pub(crate) desk: crate::desktop::Desk,
    pub(crate) search: crate::palette::Search,
    pub agent_list_open: bool,
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
    /// How many agents the window has asked the daemon to show, so only the latest refused show
    /// undoes what the window did ahead of it.
    pub(crate) shows_asked: u64,
    /// The home screen lists its snoozed agents, not only their count.
    pub home_snoozed_open: bool,
    /// The drain of each running auto-settle countdown whose agent is off screen, by agent.
    pub(crate) countdowns: std::collections::HashMap<String, crate::sidebar::Countdown>,
    pub(crate) snooze: Option<crate::sidebar::snooze::Menu>,
    pub(crate) bar_peek: crate::sidebar::bar_peek::Hover,
    pub(crate) launching: crate::picker::Launching,
    pub(crate) closing_desktop: Option<crate::desktop::Closing>,
    /// Links opened before the current desktop arrived, in the order they came.
    pub(crate) links: Vec<crate::deep_link::Link>,
    pub(crate) width: crate::sidebar::width::Width,
    pub(crate) reorder: Option<crate::sidebar::reorder::Reorder>,
    pub(crate) session_menu: Option<crate::sidebar::session_menu::Menu>,
    /// The popped-out leaf this window shows, when it is a tile window.
    pub(crate) tile: Option<crate::desktop::TileWindow>,
    pub(crate) garden: crate::garden::Garden,
    pub(crate) asks: crate::asks::Asks,
    pub(crate) attention: crate::attention::Attention,
    pub(crate) ledger: crate::ledger::Ledger,
    pub(crate) notifications: crate::notifications::Panel,
    pub(crate) update_run: crate::update_run::Banner,
    pub(crate) settings: crate::settings::Dialog,
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
    Attention,
    Asks,
    Garden,
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
/// focus, as the Tauri app's escape stack does.
pub(crate) fn escape_drawers(state: &mut State, tx: &mut Tx) -> bool {
    if crate::garden::mode(state) == crate::garden::Mode::Full {
        crate::garden::close(state);
        return true;
    }
    match state.window.newest_drawer() {
        Some(Drawer::Garden) => crate::garden::close(state),
        Some(Drawer::Asks) => crate::asks::close(state, tx),
        Some(Drawer::Attention) => crate::attention::close(state, tx),
        None => return false,
    }
    true
}

impl Window {
    pub(crate) fn go_home(&mut self) {
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

    /// Shows the current desktop. Coming from another view lands at once, as the Tauri app
    /// shows a desktop.
    pub(crate) fn show_desktop(&mut self, tx: &mut Tx) {
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
    /// window's own tile, else the current desktop's acting leaf.
    pub(crate) fn acting<'f>(&self, facts: &'f Facts) -> Option<(&'f Desktop, String)> {
        if let Some(tile) = &self.tile {
            let desktop = facts.desktops.iter().find(|d| d.id == tile.desktop)?;
            return Some((desktop, tile.leaf.clone()));
        }
        let desktop = facts.current_desktop()?;
        let leaf = self.desk.acting_leaf(desktop);
        (!leaf.is_empty()).then_some((desktop, leaf))
    }

    /// The agent the window shows: the agent of the tile it acts on.
    pub(crate) fn on_screen(&self, facts: &Facts) -> Option<String> {
        if self.view != View::Desktop {
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
        let kept = self.keyboard.as_ref().is_some_and(|holder| {
            holder.desktop == desktop.id
                && (desktop.panes.iter().any(|p| p.pane_id == holder.pane)
                    || crate::sidebar::model::leaves(&desktop.tree_json)
                        .iter()
                        .any(|leaf| matches!(leaf, crate::sidebar::model::Leaf::Tile { id, .. } if *id == holder.pane)))
        });
        if kept {
            return;
        }
        self.keyboard = desktop
            .panes
            .iter()
            .any(|p| p.pane_id == desktop.active_pane_id)
            .then(|| Holder {
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
    switched || notice || leader || label || sidebar_label
}

pub(crate) fn view(state: &State, cx: &mut Cx<State>) -> El {
    cx.controls_keep_focus();
    let appearance =
        Appearance::from_setting(state.facts.settings.get("theme").map(String::as_str));
    let t = attn_ui::tokens(appearance.is_dark(cx.system_dark()));
    cx.zoom(crate::settings::text_scale(&state.facts.settings));
    let connection = &state.facts.connection;
    let body = match connection {
        Connection::Connected | Connection::Reconnecting { .. } => {
            match state.facts.pending_migration {
                Some(pending) => centered(&t, crate::migration_gate::screen(pending, &t)),
                None => shell(state, &t, cx.env().scale),
            }
        }
        Connection::Connecting => centered(&t, connection::connecting(&t)),
        Connection::Refused(Refusal::MigrationFailed(marker)) => {
            centered(&t, crate::migration_failure::screen(state, marker, &t))
        }
        Connection::Refused(refusal) => centered(&t, connection::refused(&t, refusal)),
    };
    let banner = match connection {
        Connection::Reconnecting { start_failed } => {
            Some(connection::reconnecting(&t, start_failed.as_deref()))
        }
        _ => None,
    };
    let update_run = matches!(
        connection,
        Connection::Connected | Connection::Reconnecting { .. }
    )
    .then(|| crate::update_run::banner(state, &t))
    .flatten();
    let root = col()
        .key("window")
        .w_full()
        .h_full()
        .fill(t.bg_app)
        .on_paste_content(crate::keys::on_paste)
        .child(title_bar(state, cx, &t))
        .children(banner)
        .children(update_run)
        .child(body);
    crate::markdown::with_image_preview(root, state, cx.is_reduced_motion(), &t)
        .children(layers(state, &t))
        .children(crate::markdown::annotating::layers(state, cx, &t))
        .children(crate::markdown::sending::layers(state, cx, &t))
        .children(crate::callouts::layers(state, cx, &t))
}

/// The band the window drags by, over which the window's own controls sit; what it holds
/// comes with the queue bar and the title bar controls.
fn title_bar(state: &State, cx: &Cx<State>, t: &Tokens) -> El {
    let connected = matches!(
        state.facts.connection,
        Connection::Connected | Connection::Reconnecting { .. }
    ) && !crate::migration_gate::holds(state);
    let controls = if connected {
        crate::sidebar::title_controls(state, t)
    } else {
        Vec::new()
    };
    let middle = connected
        .then(|| crate::sidebar::title_middle(state, t))
        .flatten();
    row()
        .key("titlebar")
        .role("banner")
        .label("Title bar")
        .w_full()
        .h(TITLE_BAR_HEIGHT)
        .pl(cx.chrome_insets().leading + 8.0)
        .pr(8.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        .items_center()
        .gap(space::S2)
        .children(controls)
        .children(middle)
        .window_drag()
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.desk.window_w == Some(w) {
                tx.live_only();
                return;
            }
            state.window.desk.window_w = Some(w);
        })
        .child(crate::desktop::shelf_slot(state, t))
        .children(connected.then(|| crate::notifications::title_button(state, t)))
        .children(
            connected
                .then(|| crate::asks::title_button(state, t))
                .flatten(),
        )
        .children(instance_marker(state, t))
        .children(connected.then(|| crate::profile_switcher::pill(state, t)))
}

/// The name of a non-production instance, so its window never passes for production's.
fn instance_marker(state: &State, t: &Tokens) -> Option<El> {
    let name = state.instance.as_str();
    (name != "default" && !name.is_empty()).then(|| {
        text(name.to_owned())
            .key("titlebar.instance")
            .role("note")
            .label(name.to_owned())
            .description(format!("Instance: {name}"))
            .font(INSTANCE_TYPE)
            .color(t.warning)
            .single_line()
            .truncate()
            .max_w(96.0)
    })
}

const INSTANCE_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0).mono();

fn centered(t: &Tokens, content: El) -> El {
    col()
        .w_full()
        .grow(1.0)
        .center()
        .fill(t.bg_app)
        .child(content)
}

fn shell(state: &State, t: &Tokens, scale: f32) -> El {
    let window = &state.window;
    row()
        .key("regions")
        .w_full()
        .grow(1.0)
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.width.window == w {
                tx.live_only();
                return;
            }
            state.window.width.window = w;
        })
        .children(crate::sidebar::view(state, t))
        .child(match window.view {
            View::Home => crate::home::view(state, t),
            View::Desktop => crate::desktop::area(state, t, scale),
        })
        .children(window.drawers.iter().map(|drawer| match drawer {
            Drawer::Garden if window.shows(Drawer::Asks) => {
                crate::garden::panel(state, t).focus_return(crate::asks::DRAWER)
            }
            Drawer::Garden => crate::garden::panel(state, t),
            Drawer::Asks => crate::asks::drawer(state, t),
            Drawer::Attention => crate::attention::drawer(state, t),
        }))
}

impl Window {
    pub(crate) fn take_keyboard(&mut self, desktop: &str, pane: &str, tx: &mut Tx) {
        self.keyboard = Some(Holder {
            desktop: desktop.into(),
            pane: pane.into(),
        });
        self.queue_cursor = None;
        tx.blur();
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
    state.window.callout.is_some() || !over_tiles(state, &attn_ui::tokens(true)).is_empty()
}

fn over_tiles(state: &State, t: &Tokens) -> Vec<El> {
    let window = &state.window;
    let mut layers = Vec::new();
    match window.layer {
        Some(Layer::Palette(Palette::Search)) => layers.push(crate::palette::view(state, t)),
        Some(Layer::Palette(Palette::Files)) => layers.push(crate::opener::view(state, t)),
        Some(Layer::NewSession) => layers.extend(crate::picker::dialog(state, t)),
        Some(Layer::Overview) => layers.extend(crate::desktop::overview(state, t)),
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
        None => {}
    }
    layers.extend(crate::attention::merge_confirmation(state, t));
    layers.extend(crate::garden::frame_layer(state, t));
    crate::notebook::root_picker_overlay(state, t, &mut layers);
    layers.extend(crate::desktop::cap_menu(state, t));
    layers.extend(crate::desktop::peek(state, t));
    layers.extend(crate::desktop::header_popover(state, t));
    layers.extend(crate::sidebar::bar_peek::view(state, t));
    layers.extend(crate::sidebar::snooze::view(state, t));
    layers.extend(crate::sidebar::session_menu::view(state, t));
    layers.extend(crate::desktop::width_label(state, t));
    layers.extend(crate::desktop::drag_overlay(state, t));
    layers.extend(crate::picker::worktree_setup(state, t));
    layers
}

fn close_layer(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

fn shortcuts(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let k = attn_ui::kit(t);
    Dialog::new("shortcuts", "Keyboard shortcuts")
        .open(true)
        .width(560.0)
        .max_height(600.0)
        .on_close_tx(close_layer)
        .child(
            col()
                .key("shortcuts.list")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(
                    col()
                        .w_full()
                        .gap(space::S2)
                        .padding(space::S6)
                        .children(cheat_sheet(&state.keymap, t)),
                ),
        )
        .build(k, layers);
}

fn cheat_sheet(keymap: &Keymap, t: &Tokens) -> Vec<El> {
    let edit = rune_kit::Button::new("Edit shortcuts")
        .on_press_tx(crate::shortcut_editor::open)
        .build(attn_ui::kit(t))
        .key("shortcuts.edit");
    let categories = [
        Category::Sessions,
        Category::Panes,
        Category::Markdown,
        Category::Review,
        Category::App,
    ];
    let sections = categories.into_iter().map(|category| {
        let rows = Action::ALL
            .iter()
            .filter(|a| a.category() == category)
            .filter_map(|action| {
                let keys = keymap.label(keymap.binding(action)?);
                Some(
                    row()
                        .key(format!("shortcut.{}", action.id()))
                        .role("listitem")
                        .label(format!("{}, {keys}", action.label()))
                        .w_full()
                        .gap(space::S2)
                        .child(
                            text(action.label())
                                .font(type_::BODY)
                                .color(t.text_primary)
                                .grow(1.0),
                        )
                        .child(text(keys).font(type_::MONO_CAPTION).color(t.text_secondary)),
                )
            });
        col()
            .key(format!("shortcuts.{category:?}"))
            .role("list")
            .label(category.label())
            .w_full()
            .gap(space::S1)
            .child(section_title(category.label(), t))
            .children(rows)
    });
    std::iter::once(edit).chain(sections).collect()
}

/// A notice that only tells: a card at the window's bottom right, as the Tauri app's toast.
fn told(notice: &Notice, t: &Tokens) -> El {
    let said = text(notice.text.clone())
        .key("notice.text")
        .role("status")
        .label(notice.text.clone())
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
        .grow(1.0)
        .min_w(0.0);
    docked_bottom_right(toast_card(toast_glyph("!", t.error, t), said, t)).inert()
}

/// A notice with its offer: one button, as the Tauri app's toast row is, that runs the offer
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
            row()
                .w_full()
                .items_center()
                .gap(space::S3)
                .child(text(face).font(type_::BODY).color(ink).grow(1.0).min_w(0.0))
                .child(text(said).font(type_::LABEL).color(said_ink))
        })
        .build(attn_ui::kit(t))
        .key("notice.offer")
        .grow(1.0)
        .min_w(0.0);
    docked_bottom_right(
        toast_card(toast_glyph("✓", t.text_secondary, t), button, t).on_hover_change(
            |state: &mut State, inside, tx: &mut Tx| hold_notice(state, inside, tx),
        ),
    )
}

fn toast_glyph(glyph: &str, ink: rune::Color, t: &Tokens) -> El {
    el().w(22.0)
        .h(22.0)
        .radius(5.0)
        .border(1.0, t.border)
        .center()
        .a11y_hidden()
        .child(text(glyph.to_owned()).font(type_::MONO_CAPTION).color(ink))
}

/// The toast card, 360 points wide.
fn toast_card(glyph: El, body: El, t: &Tokens) -> El {
    raised(
        attn_ui::kit(t),
        row()
            .key("notice.card")
            .w(360.0)
            .gap(10.0)
            .items_center()
            .px(12.0)
            .py(10.0)
            .child(glyph)
            .child(body),
        10.0,
    )
}

fn docked_bottom_right(card: El) -> El {
    el().key("notice")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            col()
                .w_full()
                .h_full()
                .items_end()
                .justify_end()
                .padding(18.0)
                .child(card),
        )
}

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
    let status = text(message.clone())
        .key(format!("{key}.text"))
        .role("status")
        .label(message)
        .font(type_::BODY)
        .color(t.text_primary)
        .padding(space::S2);
    floating(key, raised(attn_ui::kit(t), status, 6.0)).inert()
}

fn floating(key: &str, surface: El) -> El {
    el().key(key).absolute(0.0, 0.0).w_full().h_full().child(
        col()
            .w_full()
            .h_full()
            .items_center()
            .justify_end()
            .padding(space::S6)
            .child(surface),
    )
}

fn section_title(title: &str, t: &Tokens) -> El {
    text(title).font(type_::SECTION).color(t.text_tertiary)
}
