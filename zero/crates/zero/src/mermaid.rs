//! Mermaid to SVG through merman, the headless Rust port; the colors follow the client's theme.
use std::sync::atomic::{AtomicU64, Ordering};

use anyhow::{Context, Result, anyhow};

/// CSS colors for the diagram's surfaces; the client fills it from its theme.
pub struct Palette {
    pub background: String,
    pub surface: String,
    pub border: String,
    pub text: String,
    pub line: String,
    pub font_family: String,
}

pub fn render_svg(source: &str, palette: &Palette) -> Result<String> {
    // Element ids are global to the page an SVG lands on; each diagram gets its own prefix.
    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let id = COUNTER.fetch_add(1, Ordering::Relaxed);
    let config = merman::MermaidConfig::from_value(serde_json::json!({
        "theme": "base",
        "darkMode": true,
        "fontFamily": palette.font_family,
        "htmlLabels": true,
        "flowchart": { "htmlLabels": true, "padding": 16 },
        "themeVariables": {
            "primaryColor": palette.surface,
            "primaryTextColor": palette.text,
            "primaryBorderColor": palette.border,
            "lineColor": palette.line,
            "secondaryColor": palette.surface,
            "secondaryTextColor": palette.text,
            "tertiaryColor": palette.background,
            "tertiaryTextColor": palette.text,
            "background": palette.background,
            "mainBkg": palette.surface,
            "nodeBorder": palette.border,
            "nodeTextColor": palette.text,
            "textColor": palette.text,
            "titleColor": palette.text,
            "edgeLabelBackground": palette.background,
            "clusterBkg": palette.background,
            "clusterBorder": palette.border,
            "noteBkgColor": palette.surface,
            "noteBorderColor": palette.border,
            "noteTextColor": palette.text,
            "actorBkg": palette.surface,
            "actorBorder": palette.border,
            "actorTextColor": palette.text,
            "signalColor": palette.line,
            "signalTextColor": palette.text,
            "labelTextColor": palette.text,
            "loopTextColor": palette.text,
            "activationBkgColor": palette.surface,
            "activationBorderColor": palette.border,
        }
    }));
    let renderer = merman::svg::HeadlessRenderer::new()
        .with_site_config(config)
        .with_vendored_text_measurer()
        .with_diagram_id(&format!("zero-{id}"));
    let pipeline = merman::svg::SvgPipeline::resvg_safe();
    let svg = renderer
        .render_svg_with_pipeline_sync(source, &pipeline)
        .context("mermaid")?
        .ok_or_else(|| anyhow!("mermaid produced no diagram"))?;
    Ok(clear_root_background(svg))
}

/// Mermaid paints the root white whatever the theme says; the card behind the diagram is the background.
fn clear_root_background(svg: String) -> String {
    let Some(open_end) = svg.find('>') else {
        return svg;
    };
    let (root, rest) = svg.split_at(open_end);
    format!("{}{rest}", root.replace("background-color:white", "background-color:transparent"))
}

/// The diagram's size in CSS pixels, read off the SVG root's viewBox.
pub fn svg_size(svg: &str) -> Option<(f32, f32)> {
    let start = svg.find("viewBox=\"")? + "viewBox=\"".len();
    let end = svg[start..].find('"')? + start;
    let mut numbers = svg[start..end].split(|c: char| c == ' ' || c == ',').filter_map(|n| n.parse::<f32>().ok());
    let (_, _, width, height) = (numbers.next()?, numbers.next()?, numbers.next()?, numbers.next()?);
    Some((width, height))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_flowchart_renders_to_svg_with_a_size() {
        let palette = Palette {
            background: "#1a1b26".into(),
            surface: "#292e42".into(),
            border: "#7aa2f7".into(),
            text: "#c0caf5".into(),
            line: "#a9b1d6".into(),
            font_family: "Helvetica".into(),
        };
        let svg = render_svg("flowchart LR\n  a --> b\n", &palette).unwrap();
        assert!(svg.contains("<svg"), "{svg}");
        let root = &svg[..svg.find('>').unwrap()];
        assert!(!root.contains("white"), "the root keeps mermaid's white background: {root}");
        let (width, height) = svg_size(&svg).expect("a viewBox");
        assert!(width > 0. && height > 0.);
    }
}
