//! Draws terminal grids at `El::effect` slots: a glyph atlas of its own (parley shaping and
//! fallback, swash rasterization, sprite rects for box drawing), one 32-byte instance per cell
//! kept in a GPU buffer and rewritten by dirty row, and a WGSL shader.

use std::borrow::Cow;
use std::collections::HashMap;

use parley::{FontFamily, FontStyle, FontWeight, PositionedLayoutItem, StyleProperty};
use rune::compose::DeviceKey;
use rune::render::EffectSite;
use swash::scale::{Render, ScaleContext, Source, StrikeWith, image::Content};
use swash::zeno::Format;

use crate::font::{FAMILY, Fonts, Metrics, fit};
use crate::grid::{
    BOLD, Cell, FAINT, Grid, INVISIBLE, ITALIC, OverlayKind, Rgb, STRIKETHROUGH, UNDERLINE, Width,
};
use crate::kitty::{Picture, Viewport, quad, source_rect};
use crate::sprites;
use crate::theme::Theme;

const ATLAS: u32 = 1024;
const EMOJI_FAMILY: &str =
    "\"Apple Color Emoji\", \"Noto Color Emoji\", Menlo, \"DejaVu Sans Mono\", monospace";

const COLOR_GLYPH: u32 = 1;
const UNDERLINED: u32 = 2;
const STRUCK: u32 = 4;
const OVERLAID: u32 = 8;
const EDGE_TOP: u32 = 16;
const EDGE_BOTTOM: u32 = 32;
const EDGE_LEFT: u32 = 64;
const EDGE_RIGHT: u32 = 128;
const BIAS: u16 = 32768;

/// The Tauri renderer's rule (`terminalGlyphFont.ts`): VS16, a keycap, or an emoji base with
/// more code points draws from the emoji font.
fn needs_emoji(text: &str) -> bool {
    let (mut vs16, mut keycap, mut zwj, mut base, mut n) = (false, false, false, false, 0);
    for c in text.chars() {
        let cp = c as u32;
        n += 1;
        vs16 |= cp == 0xFE0F;
        keycap |= cp == 0x20E3;
        zwj |= cp == 0x200D;
        base |= (0x1F1E6..=0x1F1FF).contains(&cp)
            || (0x1F3FB..=0x1F3FF).contains(&cp)
            || (0x1F000..=0x1FAFF).contains(&cp);
    }
    vs16 || keycap || (zwj && base) || (base && n > 1)
}

#[repr(C)]
#[derive(Clone, Copy, Default, bytemuck::Pod, bytemuck::Zeroable)]
struct Inst {
    cell: [u16; 2],
    span: [u16; 2],
    glyph: [u16; 4],
    off: [u16; 2],
    fg: u32,
    bg: u32,
    flags: u32,
}

const INST: u64 = std::mem::size_of::<Inst>() as u64;

/// Kitty's deepest layer, as the Tauri renderer draws it: a placement below it draws under the
/// cells' backgrounds, one from it up to zero under the text, and the rest over the text.
const KITTY_Z_UNDER_BACKGROUND: i32 = -1_073_741_824;

/// One kitty image's quad: where it draws in the grid, in device pixels, and the part of its
/// texture it shows, in texture coordinates: x, y, width, height.
#[repr(C)]
#[derive(Clone, Copy, Default, bytemuck::Pod, bytemuck::Zeroable)]
struct ImageInst {
    rect: [f32; 4],
    uv: [f32; 4],
}

const IMAGE_INST: u64 = std::mem::size_of::<ImageInst>() as u64;

/// Where the image quads of a screen go, in the order they draw.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Layer {
    UnderBackground,
    UnderText,
    OverText,
}

impl Layer {
    fn of(z: i32) -> Layer {
        if z < KITTY_Z_UNDER_BACKGROUND {
            Layer::UnderBackground
        } else if z < 0 {
            Layer::UnderText
        } else {
            Layer::OverText
        }
    }
}

#[repr(C)]
#[derive(Clone, Copy, Default, bytemuck::Pod, bytemuck::Zeroable)]
struct Uniforms {
    origin: [f32; 2],
    viewport: [f32; 2],
    cell: [f32; 2],
    atlas: [f32; 2],
    linear: f32,
    zoom: f32,
    line: f32,
    underline: f32,
}

#[derive(Clone, Copy)]
struct Slot {
    rect: [u16; 4],
    off: [u16; 2],
    color: bool,
}

#[derive(Clone, PartialEq, Eq, Hash)]
struct GlyphKey {
    text: Box<str>,
    style: u8,
    wide: bool,
}

struct Raster {
    fonts: Fonts,
    scx: ScaleContext,
    scale: f32,
    metrics: Metrics,
    baseline: f32,
}

impl Raster {
    fn new(scale: f32, metrics: Metrics) -> Raster {
        let mut r = Raster {
            fonts: Fonts::default(),
            scx: ScaleContext::new(),
            scale,
            metrics,
            baseline: 0.0,
        };
        let layout = r.layout("M", 0);
        r.baseline = layout
            .lines()
            .next()
            .map(|line| line.metrics().baseline)
            .unwrap_or(metrics.cell_h * scale * 0.75)
            .round();
        r
    }

    fn cell(&self) -> (f32, f32) {
        self.metrics.device_cell(self.scale)
    }

    fn layout(&mut self, text: &str, style: u8) -> parley::Layout<()> {
        let (fcx, lcx) = self.fonts.contexts();
        let mut b = lcx.ranged_builder(fcx, text, self.scale, true);
        let family = if needs_emoji(text) {
            EMOJI_FAMILY
        } else {
            FAMILY
        };
        b.push_default(StyleProperty::FontFamily(FontFamily::Source(
            Cow::Borrowed(family),
        )));
        b.push_default(StyleProperty::FontSize(self.metrics.font_size));
        b.push_default(StyleProperty::LineHeight(parley::LineHeight::Absolute(
            self.metrics.cell_h,
        )));
        if style & BOLD != 0 {
            b.push_default(StyleProperty::FontWeight(FontWeight::BOLD));
        }
        if style & ITALIC != 0 {
            b.push_default(StyleProperty::FontStyle(FontStyle::Italic));
        }
        let mut l = b.build(text);
        l.break_all_lines(None);
        l
    }

    /// The grapheme drawn into an RGBA canvas one or two cells wide, with how far its top-left
    /// sits from the cell's.
    fn raster(&mut self, text: &str, style: u8, wide: bool) -> Option<Canvas> {
        let (cw, ch) = self.cell();
        let span = if wide { 2.0 } else { 1.0 };
        let mut chars = text.chars();
        let cp = chars.next()? as u32;
        if chars.next().is_none()
            && let Some(rects) = sprites::rects(cp, cw * span, ch)
        {
            let (w, h) = ((cw * span) as u32, ch as u32);
            let mask = sprites::mask(&rects, w, h);
            return Some(Canvas {
                w,
                h,
                pixels: mask.iter().flat_map(|&a| [a, a, a, a]).collect(),
                off: [BIAS, BIAS],
                color: false,
            });
        }
        let margin = (cw * 0.5).round();
        let (w, h) = ((cw * span + margin * 2.0) as u32, ch as u32);
        let mut canvas = Canvas {
            w,
            h,
            pixels: vec![0u8; (w * h * 4) as usize],
            off: [BIAS - margin as u16, BIAS],
            color: false,
        };
        let mut any = false;
        let layout = self.layout(text, style);
        let baseline = self.baseline;
        for line in layout.lines() {
            for item in line.items() {
                let PositionedLayoutItem::GlyphRun(run) = item else {
                    continue;
                };
                let font = run.run().font();
                let Some(font_ref) =
                    swash::FontRef::from_index(font.data.as_ref(), font.index as usize)
                else {
                    continue;
                };
                let mut scaler = self
                    .scx
                    .builder(font_ref)
                    .size(run.run().font_size())
                    .hint(true)
                    .normalized_coords(run.run().normalized_coords().iter().map(|c| c.to_bits()))
                    .build();
                let mut x = run.offset();
                for g in run.glyphs() {
                    let pen = [margin + x + g.x, baseline + g.y];
                    x += g.advance;
                    let Some(img) = Render::new(&[
                        Source::ColorOutline(0),
                        Source::ColorBitmap(StrikeWith::BestFit),
                        Source::Outline,
                    ])
                    .format(Format::Alpha)
                    .offset(swash::zeno::Vector::new(pen[0].fract(), 0.0))
                    .render(&mut scaler, g.id as u16) else {
                        continue;
                    };
                    let p = img.placement;
                    let image = Image {
                        w: p.width as i32,
                        h: p.height as i32,
                        data: &img.data,
                        color: img.content == Content::Color,
                    };
                    if image.color && (image.w as f32) > cw * span * 1.2 {
                        let fitted = image.fit(cw * span);
                        let at = (margin as i32, ((ch - fitted.h as f32) * 0.5) as i32);
                        canvas.blit(at, &fitted.image());
                    } else {
                        let at = (
                            pen[0].floor() as i32 + p.left,
                            pen[1].round() as i32 - p.top,
                        );
                        canvas.blit(at, &image);
                    }
                    any |= image.w > 0 && image.h > 0;
                }
            }
        }
        any.then_some(canvas)
    }
}

struct Canvas {
    w: u32,
    h: u32,
    pixels: Vec<u8>,
    off: [u16; 2],
    color: bool,
}

struct Image<'a> {
    w: i32,
    h: i32,
    data: &'a [u8],
    color: bool,
}

struct Owned {
    w: i32,
    h: i32,
    data: Vec<u8>,
}

impl Owned {
    fn image(&self) -> Image<'_> {
        Image {
            w: self.w,
            h: self.h,
            data: &self.data,
            color: true,
        }
    }
}

impl Image<'_> {
    /// A color bitmap strike wider than its cells, scaled down to `width` by nearest pixel.
    fn fit(&self, width: f32) -> Owned {
        let k = width / self.w as f32;
        let (w, h) = (
            (self.w as f32 * k).round() as i32,
            (self.h as f32 * k).round() as i32,
        );
        let mut data = vec![0u8; (w * h * 4) as usize];
        for y in 0..h {
            for x in 0..w {
                let (sx, sy) = ((x as f32 / k) as i32, (y as f32 / k) as i32);
                let s = ((sy * self.w + sx) * 4) as usize;
                data[((y * w + x) * 4) as usize..][..4].copy_from_slice(&self.data[s..s + 4]);
            }
        }
        Owned { w, h, data }
    }
}

impl Canvas {
    fn blit(&mut self, (ox, oy): (i32, i32), image: &Image) {
        self.color |= image.color;
        for y in 0..image.h {
            let cy = oy + y;
            if cy < 0 || cy >= self.h as i32 {
                continue;
            }
            for x in 0..image.w {
                let cx = ox + x;
                if cx < 0 || cx >= self.w as i32 {
                    continue;
                }
                let d = ((cy as u32 * self.w + cx as u32) * 4) as usize;
                if image.color {
                    let s = ((y * image.w + x) * 4) as usize;
                    let a = u32::from(image.data[s + 3]);
                    for c in 0..3 {
                        self.pixels[d + c] = ((u32::from(image.data[s + c]) * a) / 255) as u8;
                    }
                    self.pixels[d + 3] = a as u8;
                } else {
                    let a = image.data[(y * image.w + x) as usize];
                    let v = self.pixels[d + 3].max(a);
                    self.pixels[d..d + 4].copy_from_slice(&[v, v, v, v]);
                }
            }
        }
    }
}

struct Atlas {
    pixels: Vec<u8>,
    x: u32,
    y: u32,
    row_h: u32,
    dirty: Option<[u32; 4]>,
    full: bool,
}

impl Atlas {
    fn new() -> Atlas {
        Atlas {
            pixels: vec![0; (ATLAS * ATLAS * 4) as usize],
            x: 1,
            y: 1,
            row_h: 0,
            dirty: Some([0, 0, ATLAS, ATLAS]),
            full: false,
        }
    }

    fn place(&mut self, canvas: &Canvas) -> Option<[u16; 4]> {
        let (w, h) = (canvas.w, canvas.h);
        if self.x + w + 1 > ATLAS {
            self.x = 1;
            self.y += self.row_h + 1;
            self.row_h = 0;
        }
        if self.y + h + 1 > ATLAS {
            self.full = true;
            return None;
        }
        let (x, y) = (self.x, self.y);
        let row = (w * 4) as usize;
        for r in 0..h {
            let d = (((y + r) * ATLAS + x) * 4) as usize;
            self.pixels[d..d + row]
                .copy_from_slice(&canvas.pixels[r as usize * row..(r as usize + 1) * row]);
        }
        self.x += w + 1;
        self.row_h = self.row_h.max(h);
        self.dirty = Some(match self.dirty {
            None => [x, y, x + w, y + h],
            Some(d) => [d[0].min(x), d[1].min(y), d[2].max(x + w), d[3].max(y + h)],
        });
        Some([x as u16, y as u16, w as u16, h as u16])
    }
}

/// One terminal's cells on the GPU: the background fill, one instance per cell, then the
/// cursor; and its overlays, washes first, then lines.
struct Screen {
    buf: wgpu::Buffer,
    cap: u64,
    over: wgpu::Buffer,
    over_cap: u64,
    washes: u32,
    lines: u32,
    uniforms: wgpu::Buffer,
    bind: wgpu::BindGroup,
    shape: (u16, u16),
    insts: Vec<Inst>,
    images: wgpu::Buffer,
    images_cap: u64,
    /// The cursor's instance, drawn again over an image under the text.
    cursor: Option<u32>,
    /// The picture and layer of each quad in `images`, in the order the quads draw.
    drawn: Vec<(u64, Layer)>,
}

struct Pipelines {
    format: wgpu::TextureFormat,
    bg: wgpu::RenderPipeline,
    fg: wgpu::RenderPipeline,
    underline: wgpu::RenderPipeline,
    strike: wgpu::RenderPipeline,
    over: wgpu::RenderPipeline,
    image: wgpu::RenderPipeline,
}

struct Gpu {
    key: DeviceKey,
    layout: wgpu::BindGroupLayout,
    shader: wgpu::ShaderModule,
    pipeline_layout: wgpu::PipelineLayout,
    image_layout: wgpu::BindGroupLayout,
    image_pipeline_layout: wgpu::PipelineLayout,
    pipelines: Vec<Pipelines>,
    texture: wgpu::Texture,
    view: wgpu::TextureView,
    sampler: wgpu::Sampler,
}

/// Draws terminal grids, each at the effect slot whose mark it was prepared under.
#[derive(Default)]
pub struct Cells {
    raster: Option<Raster>,
    atlas: Option<Atlas>,
    glyphs: HashMap<GlyphKey, Option<Slot>>,
    gpu: Option<Gpu>,
    screens: HashMap<u32, Screen>,
    /// A texture per kitty picture on a screen, by picture id; None for one past the device's
    /// largest texture.
    pictures: HashMap<u64, Option<wgpu::BindGroup>>,
    theme: Theme,
}

const SHADER: &str = r#"
struct U {
  origin: vec2f, viewport: vec2f, cell: vec2f, atlas: vec2f,
  linear: f32, zoom: f32, line: f32, underline: f32,
};
@group(0) @binding(0) var<uniform> u: U;
@group(0) @binding(1) var atlas_t: texture_2d<f32>;
@group(0) @binding(2) var atlas_s: sampler;

struct I {
  @location(0) cell: vec2u, @location(1) span: vec2u, @location(2) glyph: vec4u,
  @location(3) off: vec2u, @location(4) fg: u32, @location(5) bg: u32, @location(6) flags: u32,
};
struct V { @builtin(position) pos: vec4f, @location(0) color: vec4f, @location(1) uv: vec2f, @location(2) @interpolate(flat) flags: u32 };

fn corner(i: u32) -> vec2f {
  let c = array<vec2f, 6>(vec2f(0., 0.), vec2f(1., 0.), vec2f(0., 1.), vec2f(1., 0.), vec2f(1., 1.), vec2f(0., 1.));
  return c[i];
}
fn lin(c: f32) -> f32 { return select(pow((c + 0.055) / 1.055, 2.4), c / 12.92, c <= 0.04045); }
fn unpack(c: u32) -> vec4f {
  let v = vec4f(f32(c & 255u), f32((c >> 8u) & 255u), f32((c >> 16u) & 255u), f32(c >> 24u)) / 255.0;
  if (u.linear > 0.5) { return vec4f(lin(v.r), lin(v.g), lin(v.b), v.a); }
  return v;
}
fn place(p: vec2f) -> vec4f {
  let n = (u.origin + p * u.zoom) / u.viewport * 2.0 - 1.0;
  return vec4f(n.x, -n.y, 0.0, 1.0);
}

@vertex fn vs_bg(@builtin(vertex_index) vi: u32, i: I) -> V {
  var o: V;
  let bg = unpack(i.bg);
  let size = vec2f(i.span) * u.cell * select(0.0, 1.0, bg.a > 0.0);
  o.pos = place(vec2f(i.cell) * u.cell + corner(vi) * size);
  o.color = vec4f(bg.rgb * bg.a, bg.a);
  return o;
}
@fragment fn fs_bg(v: V) -> @location(0) vec4f { return v.color; }

@vertex fn vs_fg(@builtin(vertex_index) vi: u32, i: I) -> V {
  var o: V;
  let size = vec2f(i.glyph.zw);
  o.pos = place(vec2f(i.cell) * u.cell + (vec2f(i.off) - 32768.0) + corner(vi) * size);
  o.uv = (vec2f(i.glyph.xy) + corner(vi) * size) / u.atlas;
  o.color = unpack(i.fg);
  o.flags = i.flags;
  return o;
}
@fragment fn fs_fg(v: V) -> @location(0) vec4f {
  let t = textureSampleLevel(atlas_t, atlas_s, v.uv, 0.0);
  if ((v.flags & 1u) != 0u) {
    if (u.linear > 0.5) { return vec4f(lin(t.r), lin(t.g), lin(t.b), t.a); }
    return t;
  }
  let a = t.a * v.color.a;
  return vec4f(v.color.rgb * a, a);
}

fn line(vi: u32, i: I, on: bool, y: f32) -> V {
  var o: V;
  let size = vec2f(f32(i.span.x) * u.cell.x, u.line) * select(0.0, 1.0, on);
  o.pos = place(vec2f(i.cell) * u.cell + vec2f(0.0, y) + corner(vi) * size);
  let c = unpack(i.fg);
  let a = select(1.0, c.a, (i.flags & 8u) != 0u);
  o.color = vec4f(c.rgb * a, a);
  return o;
}
@vertex fn vs_underline(@builtin(vertex_index) vi: u32, i: I) -> V {
  return line(vi, i, (i.flags & 2u) != 0u, u.underline);
}
@vertex fn vs_strike(@builtin(vertex_index) vi: u32, i: I) -> V {
  return line(vi, i, (i.flags & 4u) != 0u, floor(u.cell.y / u.line * 0.5) * u.line);
}
@vertex fn vs_over(@builtin(vertex_index) vi: u32, i: I) -> V {
  if ((i.flags & 240u) == 0u) { return line(vi, i, true, u.underline); }
  var o: V;
  let width = f32(i.span.x) * u.cell.x;
  var at = vec2f(0.0, 0.0);
  var size = vec2f(width, u.line);
  if ((i.flags & 32u) != 0u) { at.y = u.cell.y - u.line; }
  if ((i.flags & 64u) != 0u) { size = vec2f(u.line, u.cell.y); }
  if ((i.flags & 128u) != 0u) { at.x = width - u.line; size = vec2f(u.line, u.cell.y); }
  o.pos = place(vec2f(i.cell) * u.cell + at + corner(vi) * size);
  let c = unpack(i.fg);
  o.color = vec4f(c.rgb * c.a, c.a);
  return o;
}
@fragment fn fs_lines(v: V) -> @location(0) vec4f { return v.color; }

@group(1) @binding(0) var image_t: texture_2d<f32>;
@group(1) @binding(1) var image_s: sampler;
struct J { @location(0) rect: vec4f, @location(1) uv: vec4f };
struct W { @builtin(position) pos: vec4f, @location(0) uv: vec2f };
@vertex fn vs_image(@builtin(vertex_index) vi: u32, j: J) -> W {
  var o: W;
  o.pos = place(j.rect.xy + corner(vi) * j.rect.zw);
  o.uv = j.uv.xy + corner(vi) * j.uv.zw;
  return o;
}
@fragment fn fs_image(w: W) -> @location(0) vec4f {
  let t = textureSampleLevel(image_t, image_s, w.uv, 0.0);
  if (u.linear > 0.5) { return vec4f(lin(t.r) * t.a, lin(t.g) * t.a, lin(t.b) * t.a, t.a); }
  return vec4f(t.rgb * t.a, t.a);
}
"#;

fn pack(c: Rgb, a: u8) -> u32 {
    u32::from(c[0]) | u32::from(c[1]) << 8 | u32::from(c[2]) << 16 | u32::from(a) << 24
}

impl Cells {
    fn gpu(&mut self, device: &wgpu::Device, key: DeviceKey) {
        if self.gpu.as_ref().is_some_and(|g| g.key == key) {
            return;
        }
        let uniform = wgpu::BindingType::Buffer {
            ty: wgpu::BufferBindingType::Uniform,
            has_dynamic_offset: false,
            min_binding_size: None,
        };
        let texture = wgpu::BindingType::Texture {
            sample_type: wgpu::TextureSampleType::Float { filterable: true },
            view_dimension: wgpu::TextureViewDimension::D2,
            multisampled: false,
        };
        let entry = |binding, visibility, ty| wgpu::BindGroupLayoutEntry {
            binding,
            visibility,
            ty,
            count: None,
        };
        let layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("terminal cells"),
            entries: &[
                entry(0, wgpu::ShaderStages::VERTEX_FRAGMENT, uniform),
                entry(1, wgpu::ShaderStages::FRAGMENT, texture),
                entry(
                    2,
                    wgpu::ShaderStages::FRAGMENT,
                    wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                ),
            ],
        });
        let shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("terminal cells"),
            source: wgpu::ShaderSource::Wgsl(SHADER.into()),
        });
        let pipeline_layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
            label: Some("terminal cells"),
            bind_group_layouts: &[Some(&layout)],
            immediate_size: 0,
        });
        let image_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("terminal images"),
            entries: &[
                entry(0, wgpu::ShaderStages::FRAGMENT, texture),
                entry(
                    1,
                    wgpu::ShaderStages::FRAGMENT,
                    wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                ),
            ],
        });
        let image_pipeline_layout =
            device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                label: Some("terminal images"),
                bind_group_layouts: &[Some(&layout), Some(&image_layout)],
                immediate_size: 0,
            });
        let texture = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("terminal glyph atlas"),
            size: wgpu::Extent3d {
                width: ATLAS,
                height: ATLAS,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8Unorm,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        let view = texture.create_view(&Default::default());
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            ..Default::default()
        });
        self.gpu = Some(Gpu {
            key,
            layout,
            shader,
            pipeline_layout,
            image_layout,
            image_pipeline_layout,
            pipelines: Vec::new(),
            texture,
            view,
            sampler,
        });
        self.screens.clear();
        self.pictures.clear();
        if let Some(atlas) = &mut self.atlas {
            atlas.dirty = Some([0, 0, ATLAS, ATLAS]);
        }
    }

    fn pipelines<'g>(
        gpu: &'g mut Gpu,
        device: &wgpu::Device,
        format: wgpu::TextureFormat,
    ) -> &'g Pipelines {
        if let Some(i) = gpu.pipelines.iter().position(|p| p.format == format) {
            return &gpu.pipelines[i];
        }
        let attrs = wgpu::vertex_attr_array![0 => Uint16x2, 1 => Uint16x2, 2 => Uint16x4, 3 => Uint16x2, 4 => Uint32, 5 => Uint32, 6 => Uint32];
        let image_attrs = wgpu::vertex_attr_array![0 => Float32x4, 1 => Float32x4];
        let cells = (&gpu.pipeline_layout, INST, &attrs[..]);
        let images = (&gpu.image_pipeline_layout, IMAGE_INST, &image_attrs[..]);
        let make = |(layout, stride, attributes): (
            &wgpu::PipelineLayout,
            u64,
            &[wgpu::VertexAttribute],
        ),
                    vs: &str,
                    fs: &str| {
            device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
                label: Some("terminal cells"),
                layout: Some(layout),
                vertex: wgpu::VertexState {
                    module: &gpu.shader,
                    entry_point: Some(vs),
                    compilation_options: Default::default(),
                    buffers: &[Some(wgpu::VertexBufferLayout {
                        array_stride: stride,
                        step_mode: wgpu::VertexStepMode::Instance,
                        attributes,
                    })],
                },
                fragment: Some(wgpu::FragmentState {
                    module: &gpu.shader,
                    entry_point: Some(fs),
                    compilation_options: Default::default(),
                    targets: &[Some(wgpu::ColorTargetState {
                        format,
                        blend: Some(wgpu::BlendState::PREMULTIPLIED_ALPHA_BLENDING),
                        write_mask: wgpu::ColorWrites::ALL,
                    })],
                }),
                primitive: Default::default(),
                depth_stencil: None,
                multisample: Default::default(),
                multiview_mask: None,
                cache: None,
            })
        };
        gpu.pipelines.push(Pipelines {
            format,
            bg: make(cells, "vs_bg", "fs_bg"),
            fg: make(cells, "vs_fg", "fs_fg"),
            underline: make(cells, "vs_underline", "fs_lines"),
            strike: make(cells, "vs_strike", "fs_lines"),
            over: make(cells, "vs_over", "fs_lines"),
            image: make(images, "vs_image", "fs_image"),
        });
        gpu.pipelines.last().expect("just pushed")
    }

    fn glyph(&mut self, grid: &Grid, cell: &Cell) -> Option<Slot> {
        let text = grid.text(cell.grapheme);
        if text.is_empty() || text == " " {
            return None;
        }
        let key = GlyphKey {
            text: text.as_ref().into(),
            style: cell.flags & (BOLD | ITALIC),
            wide: cell.width == Width::Wide,
        };
        if let Some(slot) = self.glyphs.get(&key) {
            return *slot;
        }
        let raster = self.raster.as_mut().expect("made before any glyph");
        let atlas = self.atlas.as_mut().expect("made with the raster");
        let slot = match raster.raster(&key.text, key.style, key.wide) {
            None => None,
            Some(canvas) => Some(Slot {
                rect: atlas.place(&canvas)?,
                off: canvas.off,
                color: canvas.color,
            }),
        };
        self.glyphs.insert(key, slot);
        slot
    }

    fn inst(&mut self, grid: &Grid, x: usize, y: usize) -> Inst {
        let cell = grid.row(y)[x];
        let span = if cell.width == Width::Wide { 2 } else { 1 };
        let alpha = if cell.flags & FAINT != 0 { 128 } else { 255 };
        let (mut fg, mut bg) = (pack(cell.fg, alpha), cell.bg.map_or(0, |b| pack(b, 255)));
        if grid
            .cursor
            .is_some_and(|c| (usize::from(c.col), usize::from(c.row)) == (x, y))
        {
            bg = pack(self.theme.cursor, 255);
            fg = pack(self.theme.bg, 255);
        }
        let mut flags = 0;
        if cell.flags & UNDERLINE != 0 {
            flags |= UNDERLINED;
        }
        if cell.flags & STRIKETHROUGH != 0 {
            flags |= STRUCK;
        }
        let mut inst = Inst {
            cell: [x as u16, y as u16],
            span: [span, 1],
            fg,
            bg,
            flags,
            ..Default::default()
        };
        if cell.width == Width::Spacer {
            inst.span = [0, 0];
            return inst;
        }
        if cell.flags & INVISIBLE != 0 {
            inst.flags = 0;
            return inst;
        }
        if let Some(slot) = self.glyph(grid, &cell) {
            inst.glyph = slot.rect;
            inst.off = slot.off;
            if slot.color {
                inst.flags |= COLOR_GLYPH;
            }
        }
        inst
    }

    /// Brings each grid's GPU copy up to date at `scale` device pixels per point, rewriting
    /// only the rows the grid marked dirty, and forgets every mark not given. The cursor is a
    /// block in the theme's cursor color whatever shape the program asked for, and the grid
    /// sits on the theme's background, as the Tauri terminal draws them.
    #[allow(clippy::too_many_arguments)]
    pub fn prepare<'g>(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        key: DeviceKey,
        scale: f32,
        metrics: Metrics,
        theme: Theme,
        grids: impl IntoIterator<Item = (u32, &'g mut Grid)>,
    ) {
        self.gpu(device, key);
        self.theme = theme;
        let mut grids: Vec<(u32, &mut Grid)> = grids.into_iter().collect();
        self.screens
            .retain(|mark, _| grids.iter().any(|(drawn, _)| drawn == mark));
        let rescaled = self
            .raster
            .as_ref()
            .is_none_or(|r| r.scale != scale || r.metrics != metrics);
        let full = self.atlas.as_ref().is_some_and(|a| a.full);
        if rescaled {
            self.raster = Some(Raster::new(scale, metrics));
        }
        if rescaled || full {
            self.restart_atlas();
        }
        for (mark, grid) in &mut grids {
            self.prepare_screen(device, queue, *mark, grid);
        }
        self.prepare_pictures(device, queue, &grids);
        if self.atlas.as_ref().is_some_and(|a| a.full) {
            self.restart_atlas();
            for (mark, grid) in &mut grids {
                self.prepare_screen(device, queue, *mark, grid);
            }
            if self.atlas.as_ref().is_some_and(|a| a.full) {
                eprintln!(
                    "attn-term: the glyphs on screen overflow the {ATLAS}x{ATLAS} glyph atlas; the ones past it draw blank"
                );
            }
        }
        let gpu = self.gpu.as_ref().expect("made above");
        let atlas = self.atlas.as_mut().expect("made with the raster");
        if let Some([x0, y0, x1, y1]) = atlas.dirty.take() {
            let rows: Vec<u8> = (y0..y1)
                .flat_map(|y| {
                    atlas.pixels[((y * ATLAS + x0) * 4) as usize..((y * ATLAS + x1) * 4) as usize]
                        .iter()
                        .copied()
                })
                .collect();
            queue.write_texture(
                wgpu::TexelCopyTextureInfo {
                    texture: &gpu.texture,
                    mip_level: 0,
                    origin: wgpu::Origin3d { x: x0, y: y0, z: 0 },
                    aspect: wgpu::TextureAspect::All,
                },
                &rows,
                wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some((x1 - x0) * 4),
                    rows_per_image: Some(y1 - y0),
                },
                wgpu::Extent3d {
                    width: x1 - x0,
                    height: y1 - y0,
                    depth_or_array_layers: 1,
                },
            );
        }
    }

    /// Uploads each picture a grid shows that has no texture yet, and drops the textures of
    /// pictures no grid shows.
    fn prepare_pictures(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        grids: &[(u32, &mut Grid)],
    ) {
        let shown: HashMap<u64, &Picture> = grids
            .iter()
            .flat_map(|(_, grid)| grid.images().0)
            .map(|image| (image.picture.id, image.picture.as_ref()))
            .collect();
        self.pictures.retain(|id, _| shown.contains_key(id));
        let gpu = self.gpu.as_ref().expect("made before any picture");
        for (id, picture) in shown {
            if self.pictures.contains_key(&id) {
                continue;
            }
            let largest = device.limits().max_texture_dimension_2d;
            if picture.width > largest || picture.height > largest {
                eprintln!(
                    "attn-term: a {}x{} kitty image is past this GPU's largest texture side, {largest}; it draws blank",
                    picture.width, picture.height
                );
                self.pictures.insert(id, None);
                continue;
            }
            let size = wgpu::Extent3d {
                width: picture.width,
                height: picture.height,
                depth_or_array_layers: 1,
            };
            let texture = device.create_texture(&wgpu::TextureDescriptor {
                label: Some("terminal image"),
                size,
                mip_level_count: 1,
                sample_count: 1,
                dimension: wgpu::TextureDimension::D2,
                format: wgpu::TextureFormat::Rgba8Unorm,
                usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
                view_formats: &[],
            });
            queue.write_texture(
                wgpu::TexelCopyTextureInfo {
                    texture: &texture,
                    mip_level: 0,
                    origin: wgpu::Origin3d::ZERO,
                    aspect: wgpu::TextureAspect::All,
                },
                &picture.rgba,
                wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(picture.width * 4),
                    rows_per_image: Some(picture.height),
                },
                size,
            );
            let view = texture.create_view(&Default::default());
            let bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
                label: Some("terminal image"),
                layout: &gpu.image_layout,
                entries: &[
                    wgpu::BindGroupEntry {
                        binding: 0,
                        resource: wgpu::BindingResource::TextureView(&view),
                    },
                    wgpu::BindGroupEntry {
                        binding: 1,
                        resource: wgpu::BindingResource::Sampler(&gpu.sampler),
                    },
                ],
            });
            self.pictures.insert(id, Some(bind));
        }
    }

    /// Starts the glyph atlas over, so every screen draws its glyphs into it again. A full
    /// atlas does this too: it then holds only the glyphs on screen.
    fn restart_atlas(&mut self) {
        self.atlas = Some(Atlas::new());
        self.glyphs.clear();
        for screen in self.screens.values_mut() {
            screen.shape = (0, 0);
        }
    }

    fn prepare_screen(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        mark: u32,
        grid: &mut Grid,
    ) {
        let (cols, rows) = (grid.cols, grid.rows);
        if cols == 0 || rows == 0 {
            return;
        }
        let gpu = self.gpu.as_ref().expect("made before any screen");
        let screen = self.screens.entry(mark).or_insert_with(|| {
            let uniforms = device.create_buffer(&wgpu::BufferDescriptor {
                label: Some("terminal uniforms"),
                size: std::mem::size_of::<Uniforms>() as u64,
                usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
                mapped_at_creation: false,
            });
            let bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
                label: Some("terminal cells"),
                layout: &gpu.layout,
                entries: &[
                    wgpu::BindGroupEntry {
                        binding: 0,
                        resource: uniforms.as_entire_binding(),
                    },
                    wgpu::BindGroupEntry {
                        binding: 1,
                        resource: wgpu::BindingResource::TextureView(&gpu.view),
                    },
                    wgpu::BindGroupEntry {
                        binding: 2,
                        resource: wgpu::BindingResource::Sampler(&gpu.sampler),
                    },
                ],
            });
            Screen {
                buf: device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal cells"),
                    size: INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                }),
                cap: 0,
                over: device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal overlays"),
                    size: INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                }),
                over_cap: 0,
                washes: 0,
                lines: 0,
                uniforms,
                bind,
                shape: (0, 0),
                insts: Vec::new(),
                images: device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal images"),
                    size: IMAGE_INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                }),
                images_cap: 0,
                cursor: None,
                drawn: Vec::new(),
            }
        });
        let count = usize::from(cols) * usize::from(rows) + 1;
        let reshaped = screen.shape != (cols, rows);
        if reshaped {
            if screen.cap < count as u64 {
                screen.buf = device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal cells"),
                    size: count as u64 * INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                });
                screen.cap = count as u64;
            }
            screen.insts = vec![Inst::default(); count];
            screen.shape = (cols, rows);
            grid.dirty.iter_mut().for_each(|d| *d = true);
        }
        let mut insts = std::mem::take(&mut screen.insts);
        insts[0] = Inst {
            span: [cols, rows],
            bg: pack(self.theme.bg, 255),
            ..Default::default()
        };
        let cols = usize::from(cols);
        let mut changed: Option<(usize, usize)> = None;
        for y in 0..usize::from(rows) {
            if !std::mem::take(&mut grid.dirty[y]) {
                continue;
            }
            for x in 0..cols {
                insts[1 + y * cols + x] = self.inst(grid, x, y);
            }
            changed = Some(changed.map_or((y, y), |(first, _)| (first, y)));
        }
        let screen = self.screens.get_mut(&mark).expect("inserted above");
        screen.cursor = grid
            .cursor
            .map(|c| 1 + u32::from(c.row) * cols as u32 + u32::from(c.col));
        if std::mem::take(&mut grid.images_changed) || reshaped {
            let cell = self.raster.as_ref().expect("made before any screen").cell();
            let (quads, drawn) = image_insts(grid, cell);
            screen.drawn = drawn;
            if screen.images_cap < quads.len() as u64 {
                screen.images = device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal images"),
                    size: quads.len() as u64 * IMAGE_INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                });
                screen.images_cap = quads.len() as u64;
            }
            if !quads.is_empty() {
                queue.write_buffer(&screen.images, 0, bytemuck::cast_slice(&quads));
            }
        }
        if std::mem::take(&mut grid.overlays_changed)
            || (changed.is_some() && !grid.overlays().is_empty())
        {
            let overlays = overlay_insts(grid, self.theme);
            screen.washes = overlays.0.len() as u32;
            screen.lines = overlays.1.len() as u32;
            let all: Vec<Inst> = overlays.0.into_iter().chain(overlays.1).collect();
            if screen.over_cap < all.len() as u64 {
                screen.over = device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("terminal overlays"),
                    size: all.len() as u64 * INST,
                    usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                });
                screen.over_cap = all.len() as u64;
            }
            if !all.is_empty() {
                queue.write_buffer(&screen.over, 0, bytemuck::cast_slice(&all));
            }
        }
        let write = |a: usize, b: usize| {
            queue.write_buffer(
                &screen.buf,
                a as u64 * INST,
                bytemuck::cast_slice(&insts[a..b]),
            );
        };
        write(0, 1);
        if let Some((first, last)) = changed {
            write(1 + first * cols, 1 + (last + 1) * cols);
        }
        screen.insts = insts;
    }

    /// Draws the grid prepared under `site.mark`, if there is one, into the slot's pass.
    pub fn draw(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        pass: &mut wgpu::RenderPass<'_>,
        site: &EffectSite<'_>,
    ) {
        let (Some(screen), Some(gpu), Some(raster)) = (
            self.screens.get(&site.mark),
            self.gpu.as_mut(),
            self.raster.as_ref(),
        ) else {
            return;
        };
        let count = screen.insts.len() as u32;
        if count == 0 {
            return;
        }
        let (cw, ch) = raster.cell();
        let (cols, rows) = screen.shape;
        let r = site.instance.rect;
        let fit = fit((cols, rows), (cw, ch), (r[2], r[3]));
        let line = raster.scale;
        let uniforms = Uniforms {
            origin: [
                (r[0] - site.origin[0] + fit.offset[0]).round(),
                (r[1] - site.origin[1] + fit.offset[1]).round(),
            ],
            viewport: [site.size[0] as f32, site.size[1] as f32],
            cell: [cw, ch],
            atlas: [ATLAS as f32, ATLAS as f32],
            linear: if site.srgb_blend { 0.0 } else { 1.0 },
            zoom: fit.zoom,
            line,
            underline: ((raster.metrics.font_size * 1.1).ceil() + 2.0) * line,
        };
        queue.write_buffer(&screen.uniforms, 0, bytemuck::bytes_of(&uniforms));
        let pipelines = Cells::pipelines(gpu, device, site.format);
        let pictures = &self.pictures;
        let images = |pass: &mut wgpu::RenderPass<'_>, layer: Layer| {
            let mut set = false;
            for (at, (picture, drawn)) in screen.drawn.iter().enumerate() {
                let Some(Some(bind)) = pictures.get(picture).filter(|_| *drawn == layer) else {
                    continue;
                };
                if !set {
                    pass.set_pipeline(&pipelines.image);
                    pass.set_vertex_buffer(0, screen.images.slice(..));
                    set = true;
                }
                pass.set_bind_group(1, bind, &[]);
                let at = at as u32;
                pass.draw(0..6, at..at + 1);
            }
            set
        };
        let cells = screen.buf.slice(..u64::from(count) * INST);
        pass.set_bind_group(0, &screen.bind, &[]);
        pass.set_vertex_buffer(0, cells);
        pass.set_pipeline(&pipelines.bg);
        pass.draw(0..6, 0..1);
        if images(pass, Layer::UnderBackground) {
            pass.set_vertex_buffer(0, cells);
            pass.set_pipeline(&pipelines.bg);
        }
        pass.draw(0..6, 1..count);
        if images(pass, Layer::UnderText) {
            pass.set_vertex_buffer(0, cells);
            pass.set_pipeline(&pipelines.bg);
            if let Some(cursor) = screen.cursor {
                pass.draw(0..6, cursor..cursor + 1);
            }
        }
        let (washes, lines) = (screen.washes, screen.lines);
        if washes > 0 {
            pass.set_vertex_buffer(0, screen.over.slice(..u64::from(washes + lines) * INST));
            pass.draw(0..6, 0..washes);
        }
        pass.set_vertex_buffer(0, cells);
        for pipeline in [&pipelines.fg, &pipelines.underline, &pipelines.strike] {
            pass.set_pipeline(pipeline);
            pass.draw(0..6, 1..count);
        }
        images(pass, Layer::OverText);
        if lines > 0 {
            pass.set_vertex_buffer(0, screen.over.slice(..u64::from(washes + lines) * INST));
            pass.set_pipeline(&pipelines.over);
            pass.draw(0..6, washes..washes + lines);
        }
    }
}

/// A grid's kitty images as quads in a grid of `cell`-sized cells, with the picture and layer
/// each draws from and in.
fn image_insts(grid: &Grid, cell: (f32, f32)) -> (Vec<ImageInst>, Vec<(u64, Layer)>) {
    let (images, top) = grid.images();
    let viewport = Viewport {
        top,
        cell,
        size: (f32::from(grid.cols) * cell.0, f32::from(grid.rows) * cell.1),
        device_per_unit: 1.0,
    };
    images
        .iter()
        .filter_map(|image| {
            let q = quad(&image.placed, viewport)?;
            let (w, h) = (image.picture.width, image.picture.height);
            let [x, y, sw, sh] = source_rect(&image.placed, &q, (w, h));
            let (w, h) = (w as f32, h as f32);
            let inst = ImageInst {
                rect: [q.x, q.y, q.width, q.height],
                uv: [x / w, y / h, sw / w, sh / h],
            };
            Some((inst, (image.picture.id, Layer::of(image.placed.z))))
        })
        .unzip()
}

/// A grid's overlays as instances: the washes, with the cursor drawn again over them as the
/// Tauri renderer draws it, and the lines.
fn overlay_insts(grid: &Grid, theme: Theme) -> (Vec<Inst>, Vec<Inst>) {
    let (mut washes, mut lines) = (Vec::new(), Vec::new());
    for o in grid.overlays() {
        let (start, end) = (o.cols.0.min(grid.cols), o.cols.1.min(grid.cols));
        if end <= start || o.row >= grid.rows {
            continue;
        }
        let inst = Inst {
            cell: [start, o.row],
            span: [end - start, 1],
            ..Default::default()
        };
        match o.kind {
            OverlayKind::Wash => washes.push(Inst {
                bg: pack(o.color, o.alpha),
                ..inst
            }),
            OverlayKind::Underline => lines.push(Inst {
                fg: pack(o.color, o.alpha),
                flags: UNDERLINED | OVERLAID,
                ..inst
            }),
            OverlayKind::Outline { top, bottom } => {
                let edge = |flags: u32, cell: [u16; 2], span: [u16; 2]| Inst {
                    fg: pack(o.color, o.alpha),
                    flags,
                    cell,
                    span,
                    ..Default::default()
                };
                lines.push(edge(EDGE_LEFT, [start, o.row], [1, 1]));
                lines.push(edge(EDGE_RIGHT, [end - 1, o.row], [1, 1]));
                if top {
                    lines.push(edge(EDGE_TOP, [start, o.row], [end - start, 1]));
                }
                if bottom {
                    lines.push(edge(EDGE_BOTTOM, [start, o.row], [end - start, 1]));
                }
            }
        }
    }
    if let Some(c) = grid.cursor.filter(|_| !washes.is_empty()) {
        washes.push(Inst {
            cell: [c.col, c.row],
            span: [1, 1],
            bg: pack(theme.cursor, 255),
            ..Default::default()
        });
    }
    (washes, lines)
}
