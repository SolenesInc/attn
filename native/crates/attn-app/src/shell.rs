use attn_client::{Connection, Facts};
use attn_ui::{Appearance, Tokens};
use rune::tok::{space, type_};
use rune::{Chord, Cx, El, Tx, col, el, row, text, text_input};

use crate::action::{Action, Category};
use crate::keymap::Keymap;
use crate::keys::FIELD_KEY_SUFFIX;
use crate::{State, connection};

/// A sequence's second key must follow its leader within this long, as in the Tauri app.
pub(crate) const LEADER_TIMEOUT: f64 = 0.6;
/// How long a notice stays, as the Tauri app's toast does.
const NOTICE_SECONDS: f64 = 6.0;
pub(crate) const TITLE_BAR_HEIGHT: f32 = 32.0;
const DRAWER_WIDTH: f32 = 360.0;

/// What one window shows and where its keyboard is. Desktops, the active tile and the current
/// desktop are the daemon's; this is the window's own.
#[derive(Default)]
pub struct Window {
    pub view: View,
    pub sidebar_hidden: bool,
    pub drawer: Option<Drawer>,
    pub layer: Option<Layer>,
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
    /// Wall-clock milliseconds as of the last change the window shows by time: turn ages and
    /// snooze deadlines read it, so it moves only when one of them would read differently.
    pub now_ms: i64,
    /// The queue's turns as of the last change, oldest first: where the next turn is found
    /// when the one on screen closes.
    pub(crate) queue_turns: Vec<String>,
    /// Home follows the next turn to open, as it does after the last turn closed.
    pub follow_next_turn: bool,
    /// The home screen lists its snoozed agents, not only their count.
    pub home_snoozed_open: bool,
    /// The drain of each running auto-settle countdown whose agent is off screen, by agent.
    pub(crate) countdowns: std::collections::HashMap<String, crate::sidebar::Countdown>,
    pub(crate) snooze: Option<crate::sidebar::snooze::Menu>,
    pub(crate) launching: crate::picker::Launching,
    /// Links opened before the current desktop arrived, in the order they came.
    pub(crate) links: Vec<crate::deep_link::Link>,
    pub(crate) width: crate::sidebar::width::Width,
    pub(crate) reorder: Option<crate::sidebar::reorder::Reorder>,
    pub(crate) session_menu: Option<crate::sidebar::session_menu::Menu>,
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

impl Drawer {
    fn label(self) -> &'static str {
        match self {
            Drawer::Attention => "PRs",
            Drawer::Asks => "Asks",
            Drawer::Garden => "Garden",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Layer {
    Palette(Palette),
    /// The New Session dialog; `Window::launching` holds it.
    NewSession,
    Settings,
    Shortcuts,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Palette {
    /// Agents, or commands behind a leading `>`.
    Search,
    Files,
    Sessions,
}

impl Palette {
    fn label(self) -> &'static str {
        match self {
            Palette::Search => "Search",
            Palette::Files => "Files",
            Palette::Sessions => "Sessions",
        }
    }
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
}

#[derive(Clone, Debug)]
pub(crate) struct Leader {
    pub(crate) chord: Chord,
    pub(crate) sequences: Vec<(Action, Chord)>,
    pub(crate) until: f64,
}

const PALETTE_FIELD: &str = "palette.field";

impl Window {
    pub(crate) fn go_home(&mut self) {
        self.view = View::Home;
        self.follow_next_turn = false;
    }

    /// Home after the last turn closed, ready to open the next one that opens.
    pub(crate) fn go_home_following(&mut self) {
        self.view = View::Home;
        self.follow_next_turn = true;
    }

    /// The agent the window shows: the active pane's agent of the current desktop.
    pub(crate) fn on_screen(&self, facts: &Facts) -> Option<String> {
        if self.view != View::Desktop {
            return None;
        }
        let desktop = facts.current_desktop()?;
        desktop
            .panes
            .iter()
            .find(|p| p.pane_id == desktop.active_pane_id)
            .map(|p| p.session_id.clone())
            .filter(|id| !id.is_empty())
    }

    pub(crate) fn toggle_drawer(&mut self, drawer: Drawer) {
        self.drawer = (self.drawer != Some(drawer)).then_some(drawer);
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
        let until = tx.clock() + NOTICE_SECONDS;
        self.notice = Some(Notice { text, until });
        tx.wake_at(until);
    }

    /// Keeps the keyboard on a tile of the current desktop. It moves with the current desktop
    /// and when its tile goes, to that desktop's active tile, and stays put when only the
    /// active tile changes.
    pub(crate) fn follow(&mut self, facts: &Facts) {
        self.desk.follow(facts);
        let Some(desktop) = facts.current_desktop() else {
            self.keyboard = None;
            return;
        };
        let kept = self.keyboard.as_ref().is_some_and(|holder| {
            holder.desktop == desktop.id && desktop.panes.iter().any(|p| p.pane_id == holder.pane)
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

/// Lets a notice or a sequence's leader lapse when its time is up.
pub(crate) fn expire(state: &mut State, _: f64, tx: &mut Tx) -> bool {
    let now = tx.clock();
    let window = &mut state.window;
    let notice = window.notice.take_if(|n| now >= n.until).is_some();
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
    notice || leader || label || sidebar_label
}

pub(crate) fn view(state: &State, cx: &mut Cx<State>) -> El {
    let appearance =
        Appearance::from_setting(state.facts.settings.get("theme").map(String::as_str));
    let t = attn_ui::tokens(appearance.is_dark(cx.system_dark()));
    let connection = &state.facts.connection;
    let body = match connection {
        Connection::Connected | Connection::Reconnecting => shell(state, &t, cx.env().scale),
        Connection::Connecting => centered(&t, connection::connecting(&t)),
        Connection::Refused(refusal) => centered(&t, connection::refused(&t, refusal)),
    };
    let banner = (*connection == Connection::Reconnecting).then(|| connection::reconnecting(&t));
    col()
        .key("window")
        .w_full()
        .h_full()
        .fill(t.bg_app)
        .on_paste(crate::keys::on_paste)
        .child(title_bar(state, cx, &t))
        .children(banner)
        .child(body)
        .children(layers(state, &t))
}

/// The band the window drags by, over which the window's own controls sit; what it holds
/// comes with the queue bar and the title bar controls.
fn title_bar(state: &State, cx: &Cx<State>, t: &Tokens) -> El {
    let connected = matches!(
        state.facts.connection,
        Connection::Connected | Connection::Reconnecting
    );
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
        .pl(cx.chrome_insets().leading.max(8.0))
        .pr(cx.chrome_insets().trailing.max(8.0))
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
        .children(window.drawer.map(|drawer| self::drawer(drawer, t)))
}

impl Window {
    pub(crate) fn take_keyboard(&mut self, desktop: &str, pane: &str, tx: &mut Tx) {
        self.keyboard = Some(Holder {
            desktop: desktop.into(),
            pane: pane.into(),
        });
        tx.blur();
    }
}

fn drawer(drawer: Drawer, t: &Tokens) -> El {
    col()
        .key("drawer")
        .role("complementary")
        .label(drawer.label())
        .w(DRAWER_WIDTH)
        .h_full()
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .padding(space::S3)
        .child(section_title(drawer.label(), t))
}

fn layers(state: &State, t: &Tokens) -> Vec<El> {
    let window = &state.window;
    let mut layers = Vec::new();
    match window.layer {
        Some(Layer::Palette(Palette::Search)) => layers.push(crate::palette::view(state, t)),
        Some(Layer::Palette(palette)) => {
            layers.push(self::palette(palette, &window.search.query, t))
        }
        Some(Layer::NewSession) => layers.extend(crate::picker::dialog(state, t)),
        Some(Layer::Settings) => layers.push(dialog(
            "settings",
            "Settings",
            t,
            vec![
                text("Settings are not in the native app yet.")
                    .font(type_::BODY)
                    .color(t.text_secondary),
            ],
        )),
        Some(Layer::Shortcuts) => layers.push(dialog(
            "shortcuts",
            "Keyboard shortcuts",
            t,
            cheat_sheet(&state.keymap, t),
        )),
        None => {}
    }
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
    layers.extend(crate::desktop::cap_menu(state, t));
    layers.extend(crate::sidebar::snooze::view(state, t));
    layers.extend(crate::sidebar::session_menu::view(state, t));
    layers.extend(crate::desktop::width_label(state, t));
    layers.extend(crate::desktop::drag_overlay(state, t));
    layers.extend(crate::picker::worktree_setup(state, t));
    if let Some(notice) = &window.notice {
        layers.push(floating_status("notice", notice.text.clone(), t));
    }
    layers
}

fn scrim(key: &str, t: &Tokens) -> El {
    el().key(format!("{key}.scrim"))
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .fill(t.bg_overlay)
        .a11y_hidden()
}

fn close_layer(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

fn dialog(key: &str, title: &str, t: &Tokens, body: Vec<El>) -> El {
    let panel = col()
        .key(format!("{key}.dialog"))
        .role("dialog")
        .label(title)
        .modal_tx(true, close_layer)
        .w_full()
        .max_w(560.0)
        .h_full()
        .max_h(600.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S6)
        .gap(space::S3)
        .child(text(title).font(type_::TITLE).color(t.text_primary))
        .child(
            col()
                .key("body")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .gap(space::S2)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .children(body),
        );
    el().key(key)
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(scrim(key, t))
        .child(
            col()
                .w_full()
                .h_full()
                .padding(space::S6)
                .center()
                .child(panel),
        )
}

fn palette(palette: Palette, query: &rune::TextEdit, t: &Tokens) -> El {
    let field = text_input(query, type_::BODY)
        .key(PALETTE_FIELD)
        .single_line()
        .label(palette.label())
        .placeholder(
            format!("Search {}", palette.label().to_lowercase()),
            t.text_muted,
        )
        .on_edit(|state: &mut State| &mut state.window.search.query)
        .w_full()
        .color(t.text_primary);
    debug_assert!(PALETTE_FIELD.ends_with(FIELD_KEY_SUFFIX));
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(palette.label())
        .modal_tx(true, close_layer)
        .w_full()
        .max_w(640.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S3)
        .child(field);
    el().key("palette")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(scrim("palette", t))
        .child(col().w_full().h_full().pt(80.0).items_center().child(panel))
}

fn cheat_sheet(keymap: &Keymap, t: &Tokens) -> Vec<El> {
    let categories = [
        Category::Sessions,
        Category::Panes,
        Category::Markdown,
        Category::Review,
        Category::App,
    ];
    categories
        .into_iter()
        .map(|category| {
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
        })
        .collect()
}

fn floating_status(key: &str, message: String, t: &Tokens) -> El {
    el().key(key).absolute(0.0, 0.0).w_full().h_full().child(
        col()
            .w_full()
            .h_full()
            .items_center()
            .justify_end()
            .padding(space::S6)
            .child(
                text(message.clone())
                    .key(format!("{key}.text"))
                    .role("status")
                    .label(message)
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .fill(t.bg_elevated)
                    .border(1.0, t.border)
                    .radius(6.0)
                    .padding(space::S2),
            ),
    )
}

fn section_title(title: &str, t: &Tokens) -> El {
    text(title).font(type_::SECTION).color(t.text_tertiary)
}
