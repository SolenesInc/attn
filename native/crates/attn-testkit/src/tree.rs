//! Builds a desktop's `tree_json` as the daemon writes it.

use serde_json::{Value, json};

pub fn pane(id: &str) -> Value {
    json!({"type": "pane", "pane_id": id})
}

pub fn tile(id: &str, kind: &str) -> Value {
    json!({"type": "tile", "tile_id": id, "tile_kind": kind})
}

/// `first` above `second`, each half the height.
pub fn below(first: Value, second: Value) -> Value {
    split("horizontal", first, second)
}

fn split(direction: &str, first: Value, second: Value) -> Value {
    let id = format!(
        "{direction}:{}",
        first["pane_id"]
            .as_str()
            .or(first["tile_id"].as_str())
            .or(first["split_id"].as_str())
            .unwrap_or("")
    );
    json!({"type": "split", "split_id": id, "direction": direction, "ratio": 0.5,
           "children": [first, second]})
}

/// Columns side by side, as the daemon chains them.
pub fn row(columns: impl IntoIterator<Item = Value>) -> Value {
    let mut columns: Vec<Value> = columns.into_iter().collect();
    let mut tree = columns.pop().expect("a row has a column");
    while let Some(column) = columns.pop() {
        tree = split("vertical", column, tree);
    }
    tree
}

/// The `tree_json` of `ids` as terminal panes side by side; empty for none.
pub fn panes_in_a_row(ids: &[&str]) -> String {
    if ids.is_empty() {
        return String::new();
    }
    row(ids.iter().map(|id| pane(id))).to_string()
}
