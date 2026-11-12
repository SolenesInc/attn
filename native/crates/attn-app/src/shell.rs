use std::sync::Arc;

use attn_client::{Connection, Facts};
use attn_ui::{Appearance, Tokens};
use rune::tok::{space, type_};
use rune::{Chord, Cx, El, Tx, col, el, memo, row, text, text_input};

use crate::action::{Action, Category};
use crate::keymap::Keymap;
use crate::keys::FIELD_KEY_SUFFIX;
use crate::{State, connection};

/// A sequence's second key must follow its leader within this long, as in the Tauri app.
pub(crate) const LEADER_TIMEOUT: f64 = 0.6;
/// How long a notice stays, as the Tauri app's toast does.
const NOTICE_SECONDS: f64 = 6.0;
const SIDEBAR_WIDTH: f32 = 240.0;
const DRAWER_WIDTH: f32 = 360.0;

/// What one window shows and where its keyboard is. Desktops, the active tile and the current
/// desktop are the daemon's; this is the window's own.
#[derive(Default)]
pub struct Window {
    pub sidebar_hidden: bool,
    pub drawer: Option<Drawer>,
    pub layer: Option<Layer>,
    pub notice: Option<Notice>,
    pub(crate) leader: Option<Leader>,
    /// The tile that takes keys while nothing else in the window has focus. It is apart from
    /// the active tile: another window, or the daemon, can change the active tile without
    /// taking this window's keyboard.
    pub keyboard: Option<Holder>,
    palette_query: rune::TextEdit,
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
    Settings,
    Shortcuts,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Palette {
    Agents,
    Commands,
    Files,
    Sessions,
}

impl Palette {
    fn label(self) -> &'static str {
        match self {
            Palette::Agents => "Agents",
            Palette::Commands => "Commands",
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
    pub(crate) fn toggle_drawer(&mut self, drawer: Drawer) {
        self.drawer = (self.drawer != Some(drawer)).then_some(drawer);
    }

    pub(crate) fn toggle_layer(&mut self, layer: Layer, tx: &mut Tx) {
        if self.layer == Some(layer) {
            self.layer = None;
            return;
        }
        self.layer = Some(layer);
        if let Layer::Palette(_) = layer {
            self.palette_query = rune::TextEdit::default();
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
    notice || leader
}

pub(crate) fn view(state: &State, cx: &mut Cx<State>) -> El {
    let appearance =
        Appearance::from_setting(state.facts.settings.get("theme").map(String::as_str));
    let t = attn_ui::tokens(appearance.is_dark(cx.system_dark()));
    let connection = &state.facts.connection;
    let body = match connection {
        Connection::Connected | Connection::Reconnecting => shell(state, &t),
        Connection::Connecting => centered(&t, connection::connecting(&t)),
        Connection::Refused(refusal) => centered(&t, connection::refused(&t, refusal)),
    };
    let banner = (*connection == Connection::Reconnecting).then(|| connection::reconnecting(&t));
    col()
        .key("window")
        .w_full()
        .h_full()
        .fill(t.bg_app)
        .children(banner)
        .child(body)
        .children(layers(state, &t))
}

fn centered(t: &Tokens, content: El) -> El {
    col()
        .w_full()
        .grow(1.0)
        .center()
        .fill(t.bg_app)
        .child(content)
}

fn shell(state: &State, t: &Tokens) -> El {
    let window = &state.window;
    row()
        .key("regions")
        .w_full()
        .grow(1.0)
        .children((!window.sidebar_hidden).then(|| sidebar(&state.facts, t)))
        .child(tiles(state, t))
        .children(window.drawer.map(|drawer| self::drawer(drawer, t)))
}

fn version<T>(arc: &Arc<T>) -> usize {
    Arc::as_ptr(arc) as usize
}

fn sidebar(facts: &Facts, t: &Tokens) -> El {
    let reads = (version(&facts.sessions), version(&facts.desktops), *t);
    memo("sidebar", reads, || {
        let sessions = facts.sessions.iter().map(|session| {
            row_text(&session.label, t)
                .key(format!("sidebar.session.{}", session.id))
                .role("listitem")
                .label(session.label.clone())
        });
        col()
            .key("sidebar")
            .role("navigation")
            .label("Sidebar")
            .w(SIDEBAR_WIDTH)
            .h_full()
            .fill(t.bg_panel)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .padding(space::S2)
            .gap(space::S1)
            .child(section_title("Sessions", t))
            .child(
                col()
                    .role("list")
                    .label("Sessions")
                    .gap(2.0)
                    .children(sessions),
            )
    })
}

fn tiles(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let desktop = facts.current_desktop();
    let holder = state.window.keyboard.clone();
    let reads = (
        version(&facts.desktops),
        version(&facts.profiles),
        holder.clone(),
        *t,
    );
    let area = memo("tiles.area", reads, || {
        let Some(desktop) = desktop else {
            return col()
                .w_full()
                .h_full()
                .center()
                .child(text("No desktop").font(type_::BODY).color(t.text_tertiary));
        };
        let panes = desktop.panes.iter().map(|pane| {
            let active = pane.pane_id == desktop.active_pane_id;
            let held = holder.as_ref().is_some_and(|h| h.pane == pane.pane_id);
            let pane_id = pane.pane_id.clone();
            let desktop_id = desktop.id.clone();
            col()
                .key(format!("tile.{}", pane.pane_id))
                .grow(1.0)
                .h_full()
                .radius(6.0)
                .fill(t.bg_tile)
                .border(
                    1.0,
                    if held {
                        t.accent
                    } else if active {
                        t.border_focus
                    } else {
                        t.border
                    },
                )
                .padding(space::S3)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    state.window.take_keyboard(&desktop_id, &pane_id, tx);
                })
                .role("group")
                .label(tile_name(&pane.title))
                .child(
                    text(tile_name(&pane.title))
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
        });
        row()
            .w_full()
            .h_full()
            .gap(space::S2)
            .padding(space::S2)
            .children(panes)
    });
    col()
        .key("tiles")
        .role("main")
        .label("Tiles")
        .grow(1.0)
        .h_full()
        .child(area)
}

fn tile_name(title: &str) -> String {
    if title.is_empty() {
        "Terminal".into()
    } else {
        title.into()
    }
}

impl Window {
    fn take_keyboard(&mut self, desktop: &str, pane: &str, tx: &mut Tx) {
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
        Some(Layer::Palette(palette)) => {
            layers.push(self::palette(palette, &window.palette_query, t))
        }
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

fn close_layer(state: &mut State) {
    state.window.layer = None;
}

fn dialog(key: &str, title: &str, t: &Tokens, body: Vec<El>) -> El {
    let panel = col()
        .key(format!("{key}.dialog"))
        .role("dialog")
        .label(title)
        .modal(true, close_layer)
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
        .on_edit(|state: &mut State| &mut state.window.palette_query)
        .w_full()
        .color(t.text_primary);
    debug_assert!(PALETTE_FIELD.ends_with(FIELD_KEY_SUFFIX));
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(palette.label())
        .modal(true, close_layer)
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
                .filter_map(|&action| {
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

fn row_text(label: &str, t: &Tokens) -> El {
    row()
        .w_full()
        .padding(space::S1)
        .radius(4.0)
        .child(text(label).font(type_::BODY).color(t.text_secondary))
}
