//! The tour's illustrations: each idea drawn as the window looks once it has happened.

use attn_ui::Tokens;
use rune::tok::TextStyle;
use rune::{Color, El, text};

pub(super) const WIDTH: f32 = 520.0;
const HEIGHT: f32 = 250.0;

#[derive(Clone, Copy)]
enum Agent {
    Waiting,
    Busy,
    Idle,
}

#[derive(Clone, Copy)]
enum Ink {
    Text,
    Small,
    Dim,
    SmallDim,
    Ask,
    Label,
}

#[derive(Clone, Copy)]
pub(super) enum Scene {
    Profiles,
    Desktops,
    Arrange,
    Queue,
    QueueBar,
    AgentPalette,
    CommandPalette,
}

impl Scene {
    fn label(self) -> &'static str {
        match self {
            Scene::Profiles => {
                "Switching from the Work profile to Personal swaps every agent and desktop"
            }
            Scene::Desktops => {
                "Pressing the desktop 2 shortcut swaps the panes for desktop 2's arrangement"
            }
            Scene::Arrange => {
                "Dragging web's header docks it below api; then sending web to desktop 2 takes you there with it"
            }
            Scene::Queue => {
                "Settling api takes it off the waiting list and shows web, the next agent in the queue"
            }
            Scene::QueueBar => {
                "Hiding the queue sidebar folds it into a bar across the top of the window"
            }
            Scene::AgentPalette => "The agent palette filters every agent and tile as you type",
            Scene::CommandPalette => {
                "The command palette lists actions such as turning on the agent queue or making a new desktop"
            }
        }
    }
}

struct Paint<'a> {
    t: &'a Tokens,
    out: Vec<El>,
}

impl Paint<'_> {
    #[allow(clippy::too_many_arguments)]
    fn rect(&mut self, x: f32, y: f32, w: f32, h: f32, r: f32, fill: Color, stroke: Option<Color>) {
        let mut e = rune::el().absolute(x, y).size(w, h).radius(r).fill(fill);
        if let Some(stroke) = stroke {
            e = e.border(1.0, stroke);
        }
        self.out.push(e);
    }

    fn hline(&mut self, x1: f32, x2: f32, y: f32) {
        self.rect(x1, y - 0.5, x2 - x1, 1.0, 0.0, self.t.border, None);
    }

    fn dot(&mut self, cx: f32, cy: f32, r: f32, agent: Agent) {
        let fill = match agent {
            Agent::Waiting => self.t.warning,
            Agent::Busy => self.t.success,
            Agent::Idle => self.t.text_dimmed,
        };
        self.rect(cx - r, cy - r, r * 2.0, r * 2.0, r, fill, None);
    }

    fn ink(&self, ink: Ink) -> (TextStyle, Color) {
        let t = self.t;
        match ink {
            Ink::Text => (TextStyle::new(11.0, 400.0), t.text_secondary),
            Ink::Small => (TextStyle::new(9.5, 400.0), t.text_secondary),
            Ink::Dim => (TextStyle::new(11.0, 400.0), t.text_tertiary),
            Ink::SmallDim => (TextStyle::new(9.5, 400.0), t.text_tertiary),
            Ink::Ask => (TextStyle::new(11.0, 600.0), t.text_primary),
            Ink::Label => (
                TextStyle {
                    tracking: 0.7,
                    ..TextStyle::new(8.5, 700.0)
                },
                t.text_tertiary,
            ),
        }
    }

    fn text(&mut self, x: f32, baseline: f32, s: &str, ink: Ink) {
        let (style, color) = self.ink(ink);
        self.out.push(
            text(s)
                .font(style)
                .color(color)
                .absolute(x, baseline - style.size * 0.93),
        );
    }

    fn text_end(&mut self, right: f32, baseline: f32, s: &str, ink: Ink) {
        let (style, color) = self.ink(ink);
        self.out.push(
            rune::el()
                .absolute(right - 200.0, baseline - style.size * 0.93)
                .w(200.0)
                .justify_end()
                .child(text(s).font(style).color(color)),
        );
    }

    fn sidebar(&mut self) {
        self.rect(9.0, 28.0, 132.0, 213.0, 0.0, self.t.bg_panel, None);
    }

    fn selected(&mut self, x: f32, y: f32, w: f32, h: f32, r: f32) {
        self.rect(x, y, w, h, r, self.t.bg_selected, None);
    }

    fn row(&mut self, y: f32, label: &str, agent: Agent, selected: bool) {
        if selected {
            self.selected(16.0, y - 11.0, 118.0, 18.0, 4.0);
        }
        self.dot(25.0, y - 2.0, 3.5, agent);
        self.text(35.0, y + 2.0, label, Ink::Text);
    }

    fn chip(&mut self, x: f32, y: f32, number: u8, current: bool) {
        let (fill, ink) = if current {
            (self.t.brand, Color::WHITE)
        } else {
            (self.t.bg_button, self.t.text_tertiary)
        };
        self.out.push(
            rune::el()
                .absolute(x, y - 9.0)
                .size(16.0, 13.0)
                .radius(3.0)
                .fill(fill)
                .items_center()
                .justify_center()
                .child(
                    text(number.to_string())
                        .font(TextStyle::new(8.5, 700.0))
                        .color(ink),
                ),
        );
    }

    fn desktop_rule(&mut self, y: f32, number: u8, current: bool) {
        self.chip(16.0, y, number, current);
        self.hline(38.0, 134.0, y - 2.5);
    }

    fn lines(&mut self, x: f32, y: f32, w: f32, count: usize) {
        const WIDTHS: [f32; 6] = [0.82, 0.6, 0.72, 0.45, 0.66, 0.54];
        let fill = self.t.text_muted.alpha(0.5);
        for i in 0..count {
            self.rect(
                x,
                y + i as f32 * 12.0,
                w * WIDTHS[i % WIDTHS.len()],
                4.0,
                2.0,
                fill,
                None,
            );
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn pane(
        &mut self,
        x: f32,
        y: f32,
        w: f32,
        h: f32,
        label: &str,
        agent: Agent,
        active: bool,
        lines: usize,
        ask: Option<&str>,
    ) {
        let stroke = if active {
            self.t.brand
        } else {
            self.t.border_subtle
        };
        self.rect(x, y, w, h, 6.0, self.t.bg_tile, Some(stroke));
        self.dot(x + 11.0, y + 11.0, 3.0, agent);
        self.text(x + 20.0, y + 14.0, label, Ink::Small);
        self.lines(x + 10.0, y + 28.0, w - 20.0, lines);
        if let Some(ask) = ask {
            self.text(x + 10.0, y + h - 14.0, ask, Ink::Ask);
        }
    }

    fn pill(&mut self, x: f32, y: f32, w: f32, h: f32) {
        self.rect(x, y, w, h, h / 2.0, self.t.bg_button, None);
    }

    fn desktop_one_backdrop(&mut self) {
        self.sidebar();
        self.desktop_rule(46.0, 1, true);
        self.row(64.0, "api", Agent::Waiting, true);
        self.row(84.0, "web", Agent::Busy, false);
        self.desktop_rule(110.0, 2, false);
        self.row(128.0, "docs", Agent::Busy, false);
        self.pane(
            150.0,
            38.0,
            172.0,
            194.0,
            "api",
            Agent::Waiting,
            true,
            4,
            None,
        );
        self.pane(
            330.0,
            38.0,
            172.0,
            194.0,
            "web",
            Agent::Busy,
            false,
            4,
            None,
        );
    }

    fn palette(&mut self, query: &str, rows: &[(&str, Option<Agent>, Option<&str>)], footer: &str) {
        let top = 50.0;
        self.rect(9.0, 9.0, 502.0, 232.0, 9.0, Color::BLACK.alpha(0.45), None);
        self.rect(
            110.0,
            top,
            300.0,
            164.0,
            9.0,
            self.t.bg_elevated,
            Some(self.t.border),
        );
        self.text(124.0, top + 22.0, query, Ink::Text);
        self.rect(
            126.0 + query.chars().count() as f32 * 6.4,
            top + 11.0,
            1.5,
            14.0,
            0.0,
            self.t.brand,
            None,
        );
        self.hline(110.0, 410.0, top + 34.0);
        for (i, (label, agent, hint)) in rows.iter().enumerate() {
            let y = top + 56.0 + i as f32 * 24.0;
            if i == 0 {
                self.selected(118.0, y - 14.0, 284.0, 22.0, 5.0);
            }
            match agent {
                Some(agent) => self.dot(132.0, y - 3.0, 3.5, *agent),
                None => self.text(127.0, y + 1.0, "›", Ink::Dim),
            }
            self.text(144.0, y + 1.0, label, Ink::Text);
            if let Some(hint) = hint {
                self.text_end(396.0, y + 1.0, hint, Ink::Dim);
            }
        }
        self.hline(110.0, 410.0, top + 144.0);
        self.text(124.0, top + 158.0, footer, Ink::SmallDim);
    }

    fn queue_rail(&mut self, waiting: &[&str], more: usize) {
        let head = format!("{} WAITING", waiting.len());
        let tail = format!(
            "{} {more} MORE {}",
            attn_ui::icon::CARET_RIGHT,
            if more == 1 { "AGENT" } else { "AGENTS" }
        );
        let rule_from = |label: &str| 18.0 + label.chars().count() as f32 * 6.3 + 6.0;
        let more_y = 62.0 + waiting.len() as f32 * 20.0 + 6.0;
        self.text(18.0, 44.0, &head, Ink::Label);
        self.hline(rule_from(&head), 134.0, 41.5);
        for (i, label) in waiting.iter().enumerate() {
            self.row(62.0 + i as f32 * 20.0, label, Agent::Waiting, i == 0);
        }
        self.text(18.0, more_y, &tail, Ink::Label);
        self.hline(rule_from(&tail), 134.0, more_y - 2.5);
        self.text(
            18.0,
            more_y + 16.0,
            &format!("{more} working"),
            Ink::SmallDim,
        );
    }

    fn draw(&mut self, scene: Scene) {
        use Agent::*;
        match scene {
            Scene::Profiles => {
                self.sidebar();
                self.pill(16.0, 36.0, 84.0, 18.0);
                self.text(
                    26.0,
                    49.0,
                    &format!("Personal {}", attn_ui::icon::CARET_DOWN),
                    Ink::Text,
                );
                self.desktop_rule(72.0, 1, true);
                self.row(90.0, "blog", Busy, true);
                self.row(110.0, "taxes", Waiting, false);
                self.pane(150.0, 38.0, 352.0, 194.0, "blog", Busy, true, 6, None);
            }
            Scene::Desktops => {
                self.sidebar();
                self.row(64.0, "api", Waiting, false);
                self.row(84.0, "web", Busy, false);
                self.row(128.0, "docs", Busy, false);
                self.row(148.0, "tests", Busy, false);
                self.desktop_rule(174.0, 3, false);
                self.row(192.0, "review", Idle, false);
                self.desktop_rule(46.0, 1, false);
                self.desktop_rule(110.0, 2, true);
                self.selected(16.0, 117.0, 118.0, 18.0, 4.0);
                self.row(128.0, "docs", Busy, false);
                self.pane(150.0, 38.0, 200.0, 93.0, "docs", Busy, true, 3, None);
                self.pane(150.0, 139.0, 200.0, 93.0, "tests", Busy, false, 3, None);
                self.rect(
                    358.0,
                    38.0,
                    144.0,
                    194.0,
                    6.0,
                    self.t.bg_elevated,
                    Some(self.t.border_subtle),
                );
                self.text(368.0, 52.0, "plan.md", Ink::Small);
                self.lines(368.0, 66.0, 124.0, 6);
            }
            Scene::Arrange => {
                self.sidebar();
                self.row(64.0, "api", Waiting, false);
                self.desktop_rule(46.0, 1, false);
                self.desktop_rule(90.0, 2, true);
                self.row(108.0, "tests", Busy, false);
                self.selected(16.0, 117.0, 118.0, 18.0, 4.0);
                self.row(128.0, "web", Busy, false);
                self.pane(150.0, 38.0, 172.0, 194.0, "tests", Busy, false, 4, None);
                self.pane(330.0, 38.0, 172.0, 194.0, "web", Busy, true, 4, None);
            }
            Scene::Queue => {
                self.sidebar();
                self.queue_rail(&["web", "docs"], 2);
                self.pane(
                    150.0,
                    38.0,
                    352.0,
                    194.0,
                    "web",
                    Waiting,
                    true,
                    7,
                    Some("Two tests fail on main. Fix them first?"),
                );
            }
            Scene::QueueBar => {
                self.rect(9.0, 28.0, 502.0, 24.0, 0.0, self.t.bg_panel, None);
                self.pill(16.0, 33.0, 60.0, 14.0);
                self.text(
                    24.0,
                    44.0,
                    &format!("Work {}", attn_ui::icon::CARET_DOWN),
                    Ink::Small,
                );
                self.pill(84.0, 33.0, 208.0, 14.0);
                self.dot(94.0, 40.0, 3.0, Waiting);
                self.text(
                    102.0,
                    44.0,
                    &format!("3 waiting · api › web › docs {}", attn_ui::icon::CARET_DOWN),
                    Ink::Small,
                );
                self.chip(444.0, 44.0, 1, true);
                self.chip(464.0, 44.0, 2, false);
                self.chip(484.0, 44.0, 3, false);
                self.pane(18.0, 60.0, 484.0, 172.0, "api", Waiting, true, 7, None);
            }
            Scene::AgentPalette => {
                self.desktop_one_backdrop();
                self.palette(
                    "we",
                    &[
                        ("web", Some(Waiting), Some("waiting · 2m")),
                        ("webhooks", Some(Busy), Some("desktop 3")),
                        ("weekly-report.md", None, Some("tile")),
                        ("web-e2e", Some(Idle), Some("snoozed")),
                    ],
                    "↑↓ move · ↵ jump · type > for commands",
                );
            }
            Scene::CommandPalette => {
                self.desktop_one_backdrop();
                self.palette(
                    ">",
                    &[
                        ("Turn on the agent queue", None, None),
                        ("New desktop", None, None),
                        ("Switch to Personal", None, None),
                        ("Open the notebook", None, None),
                    ],
                    "↑↓ move · ↵ run · esc closes",
                );
            }
        }
    }
}

/// `scene` as it rests, with `press` drawn as the keys that made it happen.
pub(super) fn scene(scene: Scene, press: Option<String>, t: &Tokens) -> El {
    let mut paint = Paint { t, out: Vec::new() };
    paint.rect(8.0, 8.0, 504.0, 234.0, 10.0, t.bg_app, Some(t.border));
    for cx in [22.0, 34.0, 46.0] {
        paint.rect(cx - 3.5, 14.5, 7.0, 7.0, 3.5, t.border, None);
    }
    paint.draw(scene);
    let press = press.map(|keys| {
        rune::el()
            .key("whats-new.scene.press")
            .label(keys.clone())
            .pin(None, None, Some(14.0), Some(12.0))
            .px(10.0)
            .py(6.0)
            .radius(9.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .child(
                text(keys)
                    .font(rune::tok::type_::MONO_CAPTION)
                    .color(t.text_primary)
                    .a11y_hidden(),
            )
    });
    rune::el()
        .key("whats-new.scene")
        .role("img")
        .label(scene.label())
        .size(WIDTH, HEIGHT)
        .radius(10.0)
        .clip_rounded(10.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .children(paint.out.into_iter().map(El::a11y_hidden))
        .children(press)
}
