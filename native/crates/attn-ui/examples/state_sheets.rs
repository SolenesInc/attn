//! State sheets: each control of the library in each state, side by side, drawn offscreen in
//! dark and in light, one PNG per sheet and appearance. The hover and pressed columns put the
//! pointer on the control; every other column is a gallery variant.
//!
//!     state_sheets OUT [NAME]
//!
//! With NAME, only the sheets whose name starts with it.

use std::cell::RefCell;
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use attn_ui::block_gallery::BLOCK_SUBJECTS;
use attn_ui::board_gallery::BOARD_SUBJECTS;
use attn_ui::chrome_gallery::CHROME_SHEETS;
use attn_ui::colors::scheme_for;
use attn_ui::desk_gallery::DESK_SHEETS;
use attn_ui::floating_gallery::FLOATING_SUBJECTS;
use attn_ui::gallery::{BANNERS, BUTTONS, FIELD_BOXES, SIZES, SUBJECTS};
use attn_ui::panel_gallery::{PANEL_SUBJECTS, PANEL_VARIANTS};
use attn_ui::reading_gallery::READING_SUBJECTS;
use attn_ui::settings_gallery::{SETTINGS_SUBJECTS, SETTINGS_VARIANTS};
use attn_ui::system::{Type, words};
use attn_ui::tile_gallery::TILE_SUBJECTS;
use rune::render::{Gpu, OFFSCREEN_FORMAT, Offscreen, Renderer, write_png};
use rune::{App, AppSpec, Compositor, Cx, El, Env, Frame, Key, KeyEvent, Mods, Runtime, col, el};

/// Tall enough for the List row stage at its narrowest, which stands 1034 points.
const WINDOW: (f32, f32) = (1400.0, 1100.0);
/// Wide enough for a block on a page of the widest width its story draws.
const BLOCK_WINDOW: (f32, f32) = (1500.0, 1100.0);
const SCALE: f32 = 2.0;
const MARGIN: f32 = 12.0;
const ROW_LABEL_WIDTH: f32 = 170.0;
const HEADER_HEIGHT: f32 = 30.0;

#[derive(Clone, Copy)]
enum Gesture {
    None,
    Hover,
    Press,
    Click,
    Tab,
    /// Tab in, then End: a long value scrolled to its end.
    TabEnd,
}

struct Column {
    label: &'static str,
    variant: &'static str,
    gesture: Gesture,
    aim: Aim,
}

/// What a column's gesture lands on.
#[derive(Clone, Copy)]
enum Aim {
    Row,
    /// The part whose key is the row's key with this suffix.
    Suffix(&'static str),
    /// The part with this key, anywhere in the story.
    Key(&'static str),
    /// The part with this key inside the row, where the story repeats it in other rows.
    Inside(&'static str),
}

struct Sheet {
    name: &'static str,
    story: &'static str,
    rows: Vec<(String, String)>,
    columns: Vec<Column>,
}

const fn column(label: &'static str, variant: &'static str, gesture: Gesture) -> Column {
    Column {
        label,
        variant,
        gesture,
        aim: Aim::Row,
    }
}

const fn aimed(
    label: &'static str,
    variant: &'static str,
    gesture: Gesture,
    aim: &'static str,
) -> Column {
    Column {
        label,
        variant,
        gesture,
        aim: Aim::Suffix(aim),
    }
}

const fn inside(
    label: &'static str,
    variant: &'static str,
    gesture: Gesture,
    part: &'static str,
) -> Column {
    Column {
        label,
        variant,
        gesture,
        aim: Aim::Inside(part),
    }
}

fn sheets() -> Vec<Sheet> {
    let button_rows = SIZES
        .iter()
        .flat_map(|&(size, _)| {
            BUTTONS.iter().map(move |&(look, _)| {
                (format!("{look} · {size}"), format!("button.{look}.{size}"))
            })
        })
        .collect();
    let field_rows = FIELD_BOXES
        .iter()
        .map(|key| {
            (
                key.trim_start_matches("field.").to_owned(),
                (*key).to_owned(),
            )
        })
        .collect();
    let pressable: &[(&str, &[&str])] = &[
        ("switch", &["Rest", "Focus", "Disabled"]),
        (
            "segmented",
            &["Rest", "Second chosen", "Focus", "Disabled", "Narrow"],
        ),
        ("stepper", &["Rest", "Wide value", "Focus", "Disabled"]),
        ("expander", &["Closed", "Open", "Focus", "Disabled"]),
        ("tag", &["Rest", "Long"]),
        ("icon-box", &["Rest"]),
        (
            "choice",
            &[
                "First chosen",
                "Second chosen",
                "Focus",
                "Disabled",
                "Narrow",
                "Recommended",
                "Many",
            ],
        ),
        (
            "press-area",
            &["Closed", "Open", "Focus", "Disabled", "Long"],
        ),
        (
            "disclosure",
            &["Closed", "Open", "Failed", "Focus", "Disabled"],
        ),
        (
            "trigger",
            &["Rest", "Open", "Unset", "Focus", "Disabled", "Long"],
        ),
    ];
    let still = ["tag", "icon-box"];
    let controls = SUBJECTS
        .iter()
        .zip(pressable)
        .map(|(&(story, keys), &(name, variants))| {
            let mut columns: Vec<Column> = Vec::new();
            columns.push(column(variants[0], variants[0], Gesture::None));
            if name == "stepper" {
                for (hover, pressed, aim) in [
                    ("Hover down", "Pressed down", ".down"),
                    ("Hover up", "Pressed up", ".up"),
                ] {
                    columns.push(aimed(hover, variants[0], Gesture::Hover, aim));
                    columns.push(aimed(pressed, variants[0], Gesture::Press, aim));
                }
            } else if !still.contains(&name) {
                columns.push(column("Hover", variants[0], Gesture::Hover));
                columns.push(column("Pressed", variants[0], Gesture::Press));
            }
            columns.extend(variants[1..].iter().map(|&v| match v {
                "Focus" => column("Focus", variants[0], Gesture::Tab),
                v => column(v, v, Gesture::None),
            }));
            if name == "segmented" {
                columns.extend([
                    inside("Narrow hover", "Narrow", Gesture::Hover, "segment1"),
                    inside("Narrow pressed", "Narrow", Gesture::Press, "segment1"),
                    column("Narrow focus", "Narrow", Gesture::Tab),
                    column("Narrow disabled", "Narrow disabled", Gesture::None),
                ]);
            }
            Sheet {
                name,
                story,
                rows: keys
                    .iter()
                    .map(|k| ((*k).to_owned(), (*k).to_owned()))
                    .collect(),
                columns,
            }
        });
    let mut all = vec![
        Sheet {
            name: "button",
            story: "Library/Button",
            rows: button_rows,
            columns: vec![
                column("Rest", "Rest", Gesture::None),
                column("Hover", "Rest", Gesture::Hover),
                column("Pressed", "Rest", Gesture::Press),
                column("Focus", "Rest", Gesture::Tab),
                column("Disabled", "Disabled", Gesture::None),
                column("Long label", "Long label", Gesture::None),
                column("Narrow", "Narrow", Gesture::None),
            ],
        },
        Sheet {
            name: "baseline",
            story: "Library/Baseline",
            rows: vec![("one row".to_owned(), "baseline".to_owned())],
            columns: vec![column("Rest", "Rest", Gesture::None)],
        },
        Sheet {
            name: "field-box",
            story: "Library/Field box",
            rows: field_rows,
            columns: vec![
                column("Empty", "Empty", Gesture::None),
                column("Hover", "Empty", Gesture::Hover),
                column("Filled", "Filled", Gesture::None),
                column("Focus", "Empty", Gesture::Click),
                column("Failed", "Failed", Gesture::None),
                column("Failed, focused", "Failed", Gesture::Tab),
                column("Disabled", "Disabled", Gesture::None),
                column("Disabled empty", "Disabled empty", Gesture::None),
                column("Selected text", "Selected text", Gesture::None),
                column("Long text", "Long text", Gesture::None),
                column("Long text, at its end", "Long text", Gesture::TabEnd),
                column("Narrow", "Narrow", Gesture::None),
            ],
        },
    ];
    all.extend(controls);
    all.extend(chrome_sheets());
    let banner_rows: Vec<(String, String)> = BANNERS
        .iter()
        .map(|&(tone, _)| (tone.to_owned(), format!("banner.{tone}")))
        .collect();
    all.push(Sheet {
        name: "banner-states",
        story: "Library/Banner",
        rows: banner_rows.clone(),
        columns: vec![
            column("Rest", "Rest", Gesture::None),
            aimed("Hover action", "Rest", Gesture::Hover, ".action"),
            aimed("Pressed action", "Rest", Gesture::Press, ".action"),
            aimed("Focus action", "Rest", Gesture::Tab, ".action"),
            aimed("Hover dismiss", "Rest", Gesture::Hover, ".dismiss"),
            aimed("Pressed dismiss", "Rest", Gesture::Press, ".dismiss"),
            aimed("Focus dismiss", "Rest", Gesture::Tab, ".dismiss"),
        ],
    });
    all.push(Sheet {
        name: "banner-smallest",
        story: "Library/Banner",
        rows: banner_rows.clone(),
        columns: vec![
            column("Long link", "Long link, smallest window", Gesture::None),
            aimed(
                "Focus long link",
                "Long link, smallest window",
                Gesture::Tab,
                ".go.a",
            ),
            aimed(
                "Hover long link",
                "Long link, smallest window",
                Gesture::Hover,
                ".go.a",
            ),
            aimed(
                "Focus kanji link",
                "Long link, smallest window",
                Gesture::Tab,
                ".go.b",
            ),
            column(
                "Long failure",
                "Long failure, smallest window",
                Gesture::None,
            ),
            aimed(
                "Focus Update without",
                "Long failure, smallest window",
                Gesture::Tab,
                ".second",
            ),
            column(
                "Many failures",
                "Many failures, smallest window",
                Gesture::None,
            ),
            aimed(
                "Focus Details",
                "Many failures, smallest window",
                Gesture::Tab,
                ".details",
            ),
        ],
    });
    all.push(Sheet {
        name: "banner-content",
        story: "Library/Banner",
        rows: banner_rows,
        columns: [
            "No action",
            "Sentence only",
            "Notes",
            "Countdown",
            "Two actions",
            "Inline controls",
            "Long",
        ]
        .into_iter()
        .map(|variant| column(variant, variant, Gesture::None))
        .collect(),
    });
    all.extend(FLOATING_SUBJECTS.iter().chain(TILE_SUBJECTS).map(
        |&(name, story, keys, variants, aim)| {
            let mut columns = vec![column(variants[0], variants[0], Gesture::None)];
            if let Some(aim) = aim {
                columns.push(aimed("Hover", variants[0], Gesture::Hover, aim));
                columns.push(aimed("Pressed", variants[0], Gesture::Press, aim));
            }
            columns.extend(variants[1..].iter().map(|&v| match v {
                "Focus" => column("Focus", variants[0], Gesture::Tab),
                "Failed focus" => column("Failed, focused", "Failed", Gesture::Tab),
                v => column(v, v, Gesture::None),
            }));
            Sheet {
                name,
                story,
                rows: keys
                    .iter()
                    .map(|k| ((*k).to_owned(), (*k).to_owned()))
                    .collect(),
                columns,
            }
        },
    ));
    all
}

fn gallery_view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn gallery(dark: bool, story: &str) -> Runtime<()> {
    let window = if story.starts_with("Blocks/") {
        BLOCK_WINDOW
    } else {
        WINDOW
    };
    App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("gallery", window, || (), gallery_view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(window, Env::new(dark, SCALE))
}

thread_local! {
    static LABEL: RefCell<String> = RefCell::default();
}

static GPU: LazyLock<Gpu> = LazyLock::new(Gpu::headless);

struct Label(String);

fn label_view(Label(said): &Label, cx: &mut Cx<Label>) -> El {
    let t = scheme_for(None).colors(cx.system_dark()).ui;
    el().w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(col().w_full().h_full().center().child(words(
            said.clone(),
            Type::Label,
            t.text_tertiary,
        )))
}

fn label(said: &str, size: (f32, f32), dark: bool) -> Image {
    LABEL.with_borrow_mut(|label| *label = said.to_owned());
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new(
            "label",
            size,
            || Label(LABEL.with_borrow(Clone::clone)),
            label_view,
        )
    })
    .headless_runtime(size, Env::new(dark, SCALE));
    rt.frame(0.0);
    pixels(&mut rt)
}

/// A control in one state: its rectangle on the stage with a margin round it, so its ring and
/// any layer outside its edge show.
/// The block or control `key` in `column`'s variant after its gesture, and whether the
/// keyboard reached it when the gesture is Tab.
fn cell(dark: bool, story: &str, column: &Column, key: &str) -> (Image, bool) {
    let mut rt = gallery(dark, story);
    rt.frame(0.0);
    rt.open_playground(story, Some(column.variant));
    let found = rt
        .playground_element(key)
        .unwrap_or_else(|| panic!("{story} {} has no {key}", column.variant));
    let [x, y, w, h] = found.rect;
    let aimed_at = match column.aim {
        Aim::Row | Aim::Inside(_) => None,
        Aim::Suffix(suffix) => Some(format!("{key}{suffix}")),
        Aim::Key(key) => Some(key.to_owned()),
    };
    let inside = match column.aim {
        Aim::Inside(part) => rt
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(part) && e.path.starts_with(&found.path)),
        _ => None,
    };
    let target = match (aimed_at, inside) {
        (_, Some(part)) => part,
        (Some(aimed_at), None) => rt
            .playground_element(&aimed_at)
            .unwrap_or_else(|| panic!("{story} {} has no {aimed_at}", column.variant)),
        (None, None) => found.clone(),
    };
    let [tx, ty, tw, th] = target.rect;
    let (cx, cy) = (tx + tw / 2.0, ty + th / 2.0);
    let mut focused = false;
    match column.gesture {
        Gesture::None => {}
        Gesture::Hover => rt.pointer_move(cx, cy),
        Gesture::Press => {
            rt.pointer_move(cx, cy);
            rt.pointer_down(cx, cy);
        }
        Gesture::Click => {
            rt.pointer_down(cx, cy);
            rt.pointer_up(cx, cy);
            rt.pointer_leave();
        }
        Gesture::Tab | Gesture::TabEnd if holds_tab_stop(&rt, &target.path) => {
            tab_to(&mut rt, &target.path);
            if matches!(column.gesture, Gesture::TabEnd) {
                rt.key(KeyEvent {
                    key: Key::End,
                    mods: Mods::default(),
                });
            }
            focused = true;
        }
        Gesture::Tab | Gesture::TabEnd => {}
    }
    for _ in 0..3 {
        rt.frame(1.0);
    }
    let image = pixels(&mut rt).crop([x - MARGIN, y - MARGIN, w + 2.0 * MARGIN, h + 2.0 * MARGIN]);
    (image, focused)
}

fn holds_tab_stop(rt: &Runtime<()>, path: &str) -> bool {
    rt.elements()
        .iter()
        .any(|e| e.focusable && e.path.starts_with(path))
}

/// Tab, as a person with keyboard navigation on does, until the keyboard is on `path` or
/// inside it.
fn tab_to(rt: &mut Runtime<()>, path: &str) {
    rt.set_full_keyboard_access(true);
    let stage = rt
        .elements()
        .into_iter()
        .find(|e| path.starts_with(&e.path) && e.path.ends_with("playground-stage-1"))
        .expect("the story has a stage");
    let (x, y) = (stage.rect[0] + 2.0, stage.rect[1] + 2.0);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.pointer_leave();
    rt.frame(1.0);
    for _ in 0..80 {
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
        if rt
            .elements()
            .iter()
            .any(|e| e.focus_visible && e.path.starts_with(path))
        {
            return;
        }
    }
    panic!("Tab never reached {path}");
}

struct Image {
    width: u32,
    height: u32,
    rgba: Vec<u8>,
}

impl Image {
    fn filled(width: u32, height: u32, color: [u8; 4]) -> Image {
        Image {
            width,
            height,
            rgba: color.repeat((width * height) as usize),
        }
    }

    fn crop(&self, [x, y, w, h]: [f32; 4]) -> Image {
        let px = |v: f32| (v * SCALE).round().max(0.0) as u32;
        let (x0, y0) = (px(x).min(self.width), px(y).min(self.height));
        let (x1, y1) = (
            (px(x) + px(w)).min(self.width),
            (px(y) + px(h)).min(self.height),
        );
        let mut out = Image::filled(x1 - x0, y1 - y0, [0; 4]);
        out.paste(self, -(x0 as i64), -(y0 as i64));
        out
    }

    fn paste(&mut self, from: &Image, at_x: i64, at_y: i64) {
        for row in 0..from.height as i64 {
            let y = at_y + row;
            if y < 0 || y >= self.height as i64 {
                continue;
            }
            for col in 0..from.width as i64 {
                let x = at_x + col;
                if x < 0 || x >= self.width as i64 {
                    continue;
                }
                let src = ((row * from.width as i64 + col) * 4) as usize;
                let dst = ((y * self.width as i64 + x) * 4) as usize;
                self.rgba[dst..dst + 4].copy_from_slice(&from.rgba[src..src + 4]);
            }
        }
    }
}

struct Interface;

impl<S: 'static> Compositor<S> for Interface {
    fn render(&mut self, f: &mut Frame<'_, S>) {
        let (device, queue) = (f.device, f.queue);
        let clear = f.rt.clear_color();
        let core: &mut rune::RuntimeCore = f.rt;
        f.ui.prepare(device, queue, &mut core.text.atlas, &core.instances, f.size);
        f.ui.draw_with(f.encoder, f.target, clear, &mut |_, _| {});
    }
}

fn pixels<S: 'static>(rt: &mut Runtime<S>) -> Image {
    let gpu = &*GPU;
    let (w, h) = rt.window_viewport();
    let size = ((w * SCALE).round() as u32, (h * SCALE).round() as u32);
    let format = if rt.srgb_blend() {
        OFFSCREEN_FORMAT.remove_srgb_suffix()
    } else {
        OFFSCREEN_FORMAT
    };
    let mut renderer = Renderer::new(&gpu.device, format);
    renderer.set_srgb_blend(rt.srgb_blend());
    renderer.attach_text(&mut rt.text.atlas);
    rt.text.atlas.invalidate();
    let target = Offscreen::with_format(&gpu.device, size.0, size.1, format);
    renderer.upload_images(&gpu.device, &gpu.queue, &mut rt.images);
    renderer.prepare_surfaces(&gpu.device, &gpu.queue, &rt.surfaces, size);
    let mut encoder = gpu.device.create_command_encoder(&Default::default());
    Interface.render(&mut Frame {
        device: &gpu.device,
        queue: &gpu.queue,
        encoder: &mut encoder,
        target: &target.view,
        format,
        size,
        dt: 0.0,
        update_ms: 0.0,
        rt,
        ui: &mut renderer,
    });
    target.copy(&mut encoder);
    renderer.submit(&gpu.queue, encoder);
    Image {
        width: size.0,
        height: size.1,
        rgba: target.read(&gpu.device),
    }
}

fn background(dark: bool) -> [u8; 4] {
    let hex = scheme_for(None).colors(dark).ui.bg_panel.hex();
    let byte = |i: usize| u8::from_str_radix(&hex[i..i + 2], 16).unwrap_or(0);
    [byte(1), byte(3), byte(5), 255]
}

fn shoot(sheet: &Sheet, dark: bool, out: &Path) {
    let cells: Vec<Vec<Image>> = sheet
        .rows
        .iter()
        .map(|(_, key)| {
            sheet
                .columns
                .iter()
                .map(|column| cell(dark, sheet.story, column, key).0)
                .collect()
        })
        .collect();
    let px = |v: f32| (v * SCALE).round() as u32;
    let widths: Vec<u32> = (0..sheet.columns.len())
        .map(|c| cells.iter().map(|row| row[c].width).max().unwrap_or(0))
        .collect();
    let heights: Vec<u32> = cells
        .iter()
        .map(|row| row.iter().map(|cell| cell.height).max().unwrap_or(0))
        .collect();
    let (label_w, header_h) = (px(ROW_LABEL_WIDTH), px(HEADER_HEIGHT));
    let total_w = label_w + widths.iter().sum::<u32>();
    let total_h = header_h + heights.iter().sum::<u32>();
    let mut image = Image::filled(total_w, total_h, background(dark));
    let mut x = label_w;
    for (c, column) in sheet.columns.iter().enumerate() {
        let size = (widths[c] as f32 / SCALE, HEADER_HEIGHT);
        image.paste(&label(column.label, size, dark), x as i64, 0);
        x += widths[c];
    }
    let mut y = header_h;
    for (r, (name, _)) in sheet.rows.iter().enumerate() {
        let size = (ROW_LABEL_WIDTH, heights[r] as f32 / SCALE);
        image.paste(&label(name, size, dark), 0, y as i64);
        let mut x = label_w;
        for (c, cell) in cells[r].iter().enumerate() {
            image.paste(cell, x as i64, (y + (heights[r] - cell.height) / 2) as i64);
            x += widths[c];
        }
        y += heights[r];
    }
    let appearance = if dark { "dark" } else { "light" };
    let path = out.join(format!("sheet-{}-{appearance}.png", sheet.name));
    write_png(&path, image.width, image.height, &image.rgba)
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    println!("{}", path.display());
}

/// A state a block's sheet draws, and the part its focused twin puts the keyboard on: the
/// block's first control unless the state is about another.
#[derive(Clone, Copy)]
struct State(&'static str, Aim);

const fn state(variant: &'static str) -> State {
    State(variant, Aim::Row)
}

/// The variants a block's sheet draws, one under another, after the block at rest with the
/// keyboard on its first control, each again with the keyboard on it where it takes the
/// keyboard.
const BLOCK_VARIANTS: &[(&str, &[State])] = &[
    (
        "Blocks/Page",
        &[
            state("Saving"),
            State("Refused", Aim::Key("row.number.control")),
            state("Off"),
        ],
    ),
    ("Blocks/Section", &[]),
    ("Blocks/Setting row", &[]),
    (
        "Blocks/Field",
        &[state("Saved"), state("Failed"), state("Disabled")],
    ),
    ("Blocks/Notice and status", &[]),
    (
        "Blocks/Choices",
        &[state("Second chosen"), state("Disabled")],
    ),
    (
        "Blocks/Disclosure",
        &[state("Open"), state("Failed"), state("Disabled")],
    ),
    ("Blocks/Actions", &[]),
    (
        "Blocks/Table",
        &[
            state("Open"),
            state("Sub-row open"),
            state("Off"),
            state("Empty"),
            state("Empty off"),
            state("Empty smallest long"),
            state("Empty off smallest long"),
            state("Open narrow"),
        ],
    ),
    ("Blocks/Diagnostics table", &[]),
];

/// A block's sheet: each state on a row of its own, since a block is as wide as a page.
fn block_sheet(story: &str) -> String {
    let name = story
        .trim_start_matches("Blocks/")
        .trim_start_matches("Panels/")
        .to_lowercase()
        .replace(' ', "-");
    format!("block-{name}")
}

fn shoot_block(story: &str, key: &str, extra: &[State], dark: bool, out: &Path) {
    let mut rows: Vec<(String, Column)> = vec![
        ("Rest".into(), column("", "Rest", Gesture::None)),
        ("Focus (Tab)".into(), column("", "Rest", Gesture::Tab)),
    ];
    rows.extend(extra.iter().flat_map(|&State(v, aim)| {
        [
            (v.to_owned(), column("", v, Gesture::None)),
            (
                format!("{v}, focus (Tab)"),
                Column {
                    aim,
                    ..column("", v, Gesture::Tab)
                },
            ),
        ]
    }));
    rows.extend(
        ["Long", "Narrow", "Narrow long", "Smallest long"]
            .map(|v| (v.to_owned(), column("", v, Gesture::None))),
    );
    let (rows, cells): (Vec<(String, Column)>, Vec<Image>) = rows
        .into_iter()
        .filter_map(|(name, column)| {
            let (image, focused) = cell(dark, story, &column, key);
            (focused || !matches!(column.gesture, Gesture::Tab | Gesture::TabEnd))
                .then_some(((name, column), image))
        })
        .unzip();
    let px = |v: f32| (v * SCALE).round() as u32;
    let label_w = px(ROW_LABEL_WIDTH);
    let total_w = label_w + cells.iter().map(|c| c.width).max().unwrap_or(0);
    let total_h = cells.iter().map(|c| c.height).sum::<u32>();
    let mut image = Image::filled(total_w, total_h, background(dark));
    let mut y = 0;
    for ((name, _), cell) in rows.iter().zip(&cells) {
        let size = (ROW_LABEL_WIDTH, cell.height as f32 / SCALE);
        image.paste(&label(name, size, dark), 0, y as i64);
        image.paste(cell, label_w as i64, y as i64);
        y += cell.height;
    }
    let appearance = if dark { "dark" } else { "light" };
    let path = out.join(format!("{}-{appearance}.png", block_sheet(story)));
    write_png(&path, image.width, image.height, &image.rgba)
        .unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    println!("{}", path.display());
}

fn main() {
    let out: PathBuf = std::env::args()
        .nth(1)
        .unwrap_or_else(|| panic!("usage: state_sheets OUT"))
        .into();
    std::fs::create_dir_all(&out).unwrap_or_else(|e| panic!("{}: {e}", out.display()));
    let only = std::env::args().nth(2);
    let wanted = |name: &str| only.as_deref().is_none_or(|only| name.starts_with(only));
    for sheet in sheets().into_iter().filter(|sheet| wanted(sheet.name)) {
        for dark in [true, false] {
            shoot(&sheet, dark, &out);
        }
    }
    let at_rest = |extra: &[&'static str]| extra.iter().map(|&v| state(v)).collect::<Vec<_>>();
    let blocks = BLOCK_SUBJECTS
        .iter()
        .zip(BLOCK_VARIANTS)
        .map(|(&(story, keys), &(_, extra))| (story, keys, extra.to_vec()))
        .chain(
            SETTINGS_SUBJECTS
                .iter()
                .zip(SETTINGS_VARIANTS)
                .chain(PANEL_SUBJECTS.iter().zip(PANEL_VARIANTS))
                .map(|(&(story, keys), &(_, extra))| (story, keys, at_rest(extra))),
        )
        .chain(
            READING_SUBJECTS
                .iter()
                .chain(BOARD_SUBJECTS)
                .map(|&(story, keys, extra)| (story, keys, at_rest(extra))),
        );
    for (story, keys, extra) in blocks {
        if !wanted(&block_sheet(story)) {
            continue;
        }
        for dark in [true, false] {
            shoot_block(story, keys[0], &extra, dark, &out);
        }
    }
}

/// The sheets of the chrome's controls, as `attn_ui::chrome_gallery` lists them.
fn chrome_sheets() -> impl Iterator<Item = Sheet> {
    CHROME_SHEETS.iter().chain(DESK_SHEETS).map(|sheet| Sheet {
        name: sheet.name,
        story: sheet.story,
        rows: sheet
            .keys
            .iter()
            .map(|k| ((*k).to_owned(), (*k).to_owned()))
            .collect(),
        columns: sheet
            .columns
            .iter()
            .map(|&(label, variant, gesture, aim)| Column {
                label,
                variant,
                gesture: match gesture {
                    "hover" => Gesture::Hover,
                    "press" => Gesture::Press,
                    "tab" => Gesture::Tab,
                    _ => Gesture::None,
                },
                aim: if aim.is_empty() {
                    Aim::Row
                } else {
                    Aim::Suffix(aim)
                },
            })
            .collect(),
    })
}
