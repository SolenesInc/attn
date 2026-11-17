//! The tour's illustrations: each idea drawn as the window looks once it has happened.

use attn_ui::Tokens;
use rune::tok::TextStyle;
use rune::{Color, El, text};

/// The Tauri scenes' `viewBox="0 0 520 250"`, which each scene is painted in.
const PAINTED_WIDTH: f32 = 520.0;
const HEIGHT: f32 = 250.0;

/// The width a scene is drawn at: `.whats-new-step`'s 552 point column, which the Tauri SVG
/// fills.
pub(super) const WIDTH: f32 = 552.0;

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
    Ask,
}

#[derive(Clone, Copy)]
pub(super) enum Scene {
    NewLook,
    Night,
}

impl Scene {
    fn label(self) -> &'static str {
        match self {
            Scene::NewLook => {
                "attn's window in the colors in effect: api waits for you while web and docs work"
            }
            Scene::Night => "The same window in Night's colors, on indigo surfaces",
        }
    }

    /// The colors the scene is painted in: Night's own for Night, the window's for the rest.
    fn tokens(self, t: &Tokens) -> Tokens {
        match self {
            Scene::NewLook => *t,
            Scene::Night => attn_ui::colors::scheme_for(Some("night")).colors(t.dark).ui,
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

    fn agent_color(&self, agent: Agent) -> Color {
        match agent {
            Agent::Waiting => self.t.state_waiting,
            Agent::Busy => self.t.state_working,
            Agent::Idle => self.t.state_idle,
        }
    }

    fn dot(&mut self, cx: f32, cy: f32, r: f32, agent: Agent) {
        let fill = self.agent_color(agent);
        self.rect(cx - r, cy - r, r * 2.0, r * 2.0, r, fill, None);
    }

    fn ink(&self, ink: Ink) -> (TextStyle, Color) {
        let t = self.t;
        match ink {
            Ink::Text => (TextStyle::new(11.0, 400.0), t.text_secondary),
            Ink::Small => (TextStyle::new(9.5, 400.0), t.text_secondary),
            Ink::Ask => (TextStyle::new(11.0, 600.0), t.text_primary),
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

    fn sidebar(&mut self) {
        self.rect(9.0, 28.0, 132.0, 213.0, 0.0, self.t.bg_panel, None);
    }

    fn row(&mut self, y: f32, label: &str, agent: Agent, selected: bool) {
        if selected {
            self.rect(16.0, y - 11.0, 118.0, 18.0, 4.0, self.t.bg_selected, None);
        }
        self.dot(25.0, y - 2.0, 3.5, agent);
        self.text(35.0, y + 2.0, label, Ink::Text);
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
    fn pane(&mut self, x: f32, y: f32, w: f32, h: f32, label: &str, agent: Agent, lines: usize) {
        let stroke = match agent {
            Agent::Waiting => self.t.state_waiting,
            _ => self.t.border_subtle,
        };
        self.rect(x, y, w, h, 6.0, self.t.bg_tile, Some(stroke));
        self.dot(x + 11.0, y + 11.0, 3.0, agent);
        self.text(x + 20.0, y + 14.0, label, Ink::Small);
        self.lines(x + 10.0, y + 28.0, w - 20.0, lines);
    }

    fn button(&mut self, x: f32, y: f32, label: &str) {
        self.out.push(
            rune::el()
                .absolute(x, y)
                .h(18.0)
                .px(9.0)
                .radius(5.0)
                .fill(self.t.brand)
                .items_center()
                .child(
                    text(label)
                        .font(TextStyle::new(9.5, 600.0))
                        .color(attn_ui::brand_ink(self.t)),
                ),
        );
    }

    fn draw(&mut self) {
        use Agent::*;
        self.sidebar();
        self.row(48.0, "api", Waiting, true);
        self.row(68.0, "web", Busy, false);
        self.row(88.0, "docs", Busy, false);
        self.row(108.0, "review", Idle, false);
        self.pane(150.0, 38.0, 172.0, 194.0, "api", Waiting, 5);
        self.text(160.0, 196.0, "Ship the new look?", Ink::Ask);
        self.button(160.0, 206.0, "Ship it");
        self.pane(330.0, 38.0, 172.0, 194.0, "web", Busy, 7);
    }
}

/// `scene` drawn in its colors.
pub(super) fn scene(scene: Scene, window: &Tokens) -> El {
    let tokens = scene.tokens(window);
    let t = &tokens;
    let mut paint = Paint { t, out: Vec::new() };
    paint.rect(8.0, 8.0, 504.0, 234.0, 10.0, t.bg_app, Some(t.border));
    for cx in [22.0, 34.0, 46.0] {
        paint.rect(cx - 3.5, 14.5, 7.0, 7.0, 3.5, t.border, None);
    }
    paint.draw();
    let drawn = WIDTH / PAINTED_WIDTH;
    let painted = rune::el()
        .key("whats-new.scene")
        .role("img")
        .label(scene.label())
        .size(PAINTED_WIDTH, HEIGHT)
        .radius(10.0)
        .clip_rounded(10.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .children(paint.out.into_iter().map(El::a11y_hidden))
        .scale(drawn);
    rune::el()
        .size(WIDTH, HEIGHT * drawn)
        .center()
        .child(painted)
}
