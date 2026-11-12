use std::path::Path;

use skrifa::{FontRef, MetadataProvider};

/// Every character the app's string literals draw, outside ASCII, with where it appears.
fn drawn_characters(dir: &Path, found: &mut Vec<(char, String)>) {
    for entry in std::fs::read_dir(dir).expect("the source folder reads") {
        let path = entry.expect("a source entry").path();
        if path.is_dir() {
            drawn_characters(&path, found);
            continue;
        }
        if path.extension().is_none_or(|ext| ext != "rs") {
            continue;
        }
        let source = std::fs::read_to_string(&path).expect("a source file reads");
        for (n, line) in source.lines().enumerate() {
            if line.trim_start().starts_with("//") {
                continue;
            }
            let at = || format!("{}:{}", path.display(), n + 1);
            for literal in line.split('"').skip(1).step_by(2) {
                for c in literal.chars().filter(|c| !c.is_ascii()) {
                    found.push((c, at()));
                }
            }
            let chars: Vec<char> = line.chars().collect();
            for window in chars.windows(3) {
                if let ['\'', c, '\''] = *window
                    && !c.is_ascii()
                {
                    found.push((c, at()));
                }
            }
        }
    }
}

#[test]
fn every_character_the_app_draws_is_in_a_font_it_bundles() {
    let fonts: Vec<FontRef> = attn_ui::FONTS
        .iter()
        .map(|data| FontRef::new(data).expect("a bundled font parses"))
        .collect();
    let covered = |c: char| fonts.iter().any(|font| font.charmap().map(c).is_some());
    let mut found = Vec::new();
    for crate_dir in ["", "../attn-ui"] {
        let src = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join(crate_dir)
            .join("src");
        drawn_characters(&src, &mut found);
    }
    let missing: Vec<String> = found
        .into_iter()
        .filter(|&(c, _)| !covered(c))
        .map(|(c, at)| format!("{c:?} (U+{:04X}) at {at}", c as u32))
        .collect();
    assert!(
        missing.is_empty(),
        "these characters fall back to the system's fonts, and draw as a missing glyph where it \
         has none; use a glyph from attn_ui::icon or one the bundled fonts carry:\n{}",
        missing.join("\n")
    );
}
