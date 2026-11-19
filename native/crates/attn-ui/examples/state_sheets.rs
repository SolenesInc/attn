//! State sheets: each control of the library in each state, side by side, drawn offscreen in
//! dark and in light, one PNG per sheet and appearance. The hover and pressed columns put the
//! pointer on the control; every other column is a gallery variant.
//!
//!     state_sheets OUT

use std::cell::RefCell;
use std::path::{Path, PathBuf};

use attn_ui::colors::scheme_for;
use attn_ui::gallery::{BUTTONS, FIELD_BOXES, SIZES, SUBJECTS};
use attn_ui::system::{Type, words};
use rune::render::{Gpu, OFFSCREEN_FORMAT, Offscreen, Renderer, write_png};
use rune::{App, AppSpec, Compositor, Cx, El, Env, Frame, Key, KeyEvent, Mods, Runtime, col, el};

const WINDOW: (f32, f32) = (1100.0, 800.0);
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
}

struct Column {
    label: &'static str,
    variant: &'static str,
    gesture: Gesture,
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
        ("stepper", &["Rest", "Focus", "Disabled"]),
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
    ];
    let still = ["tag", "icon-box"];
    let controls = SUBJECTS
        .iter()
        .zip(pressable)
        .map(|(&(story, keys), &(name, variants))| {
            let mut columns: Vec<Column> = Vec::new();
            columns.push(column(variants[0], variants[0], Gesture::None));
            if !still.contains(&name) {
                columns.push(column("Hover", variants[0], Gesture::Hover));
                columns.push(column("Pressed", variants[0], Gesture::Press));
            }
            columns.extend(variants[1..].iter().map(|&v| match v {
                "Focus" => column("Focus", variants[0], Gesture::Tab),
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
                column("Disabled", "Disabled", Gesture::None),
                column("Disabled empty", "Disabled empty", Gesture::None),
                column("Selected text", "Selected text", Gesture::None),
                column("Long text", "Long text", Gesture::None),
                column("Narrow", "Narrow", Gesture::None),
            ],
        },
    ];
    all.extend(controls);
    all
}

fn gallery_view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn gallery(dark: bool) -> Runtime<()> {
    App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("gallery", WINDOW, || (), gallery_view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(dark, SCALE))
}

thread_local! {
    static LABEL: RefCell<String> = RefCell::default();
    static GPU: Gpu = Gpu::headless();
}

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
fn cell(dark: bool, story: &str, column: &Column, key: &str) -> Image {
    let mut rt = gallery(dark);
    rt.frame(0.0);
    rt.open_playground(story, Some(column.variant));
    let found = rt
        .playground_element(key)
        .unwrap_or_else(|| panic!("{story} {} has no {key}", column.variant));
    let [x, y, w, h] = found.rect;
    let (cx, cy) = (x + w / 2.0, y + h / 2.0);
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
        Gesture::Tab => tab_to(&mut rt, &found.path),
    }
    for _ in 0..3 {
        rt.frame(1.0);
    }
    pixels(&mut rt).crop([x - MARGIN, y - MARGIN, w + 2.0 * MARGIN, h + 2.0 * MARGIN])
}

/// Tab, as a person does, until the keyboard is on `path` or inside it.
fn tab_to(rt: &mut Runtime<()>, path: &str) {
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
    GPU.with(|gpu| {
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
    })
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
                .map(|column| cell(dark, sheet.story, column, key))
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

fn main() {
    let out: PathBuf = std::env::args()
        .nth(1)
        .unwrap_or_else(|| panic!("usage: state_sheets OUT"))
        .into();
    std::fs::create_dir_all(&out).unwrap_or_else(|e| panic!("{}: {e}", out.display()));
    for sheet in sheets() {
        for dark in [true, false] {
            shoot(&sheet, dark, &out);
        }
    }
}
