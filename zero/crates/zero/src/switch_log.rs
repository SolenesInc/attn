use std::collections::BTreeMap;
use std::fs::{self, File, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum SwitchPath {
    Queue,
    AutoNext,
    Switcher,
    Desktop,
    PaneMove,
    Click,
    Back,
    Settle,
}

impl SwitchPath {
    pub fn label(self) -> &'static str {
        match self {
            Self::Queue => "queue",
            Self::AutoNext => "auto-next",
            Self::Switcher => "switcher",
            Self::Desktop => "desktop",
            Self::PaneMove => "pane-move",
            Self::Click => "click",
            Self::Back => "back",
            Self::Settle => "settle",
        }
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct SwitchRecord {
    #[serde(alias = "timestamp_ms")]
    pub timestamp: u64,
    pub from: Option<String>,
    pub to: Option<String>,
    pub path: SwitchPath,
    pub scenario: String,
    pub waiting_count: usize,
}

impl SwitchRecord {
    pub fn json_line(&self) -> Result<String> {
        Ok(format!("{}\n", serde_json::to_string(self)?))
    }
}

pub struct SwitchLog {
    path: PathBuf,
    file: File,
}

impl SwitchLog {
    pub fn open_default() -> Result<Self> {
        let home = std::env::var_os("HOME").context("HOME is not set")?;
        Self::open(PathBuf::from(home).join(".local/state/attn-zero/switch-log.jsonl"))
    }

    pub fn open(path: PathBuf) -> Result<Self> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).with_context(|| format!("creating {}", parent.display()))?;
        }
        let file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
            .with_context(|| format!("opening {}", path.display()))?;
        Ok(Self { path, file })
    }

    pub fn append(&mut self, record: &SwitchRecord) -> Result<()> {
        self.file.write_all(record.json_line()?.as_bytes())?;
        self.file.flush()?;
        Ok(())
    }

    pub fn path(&self) -> &Path {
        &self.path
    }
}

pub fn default_path() -> Result<PathBuf> {
    let home = std::env::var_os("HOME").context("HOME is not set")?;
    Ok(PathBuf::from(home).join(".local/state/attn-zero/switch-log.jsonl"))
}

pub fn summary(path: &Path) -> Result<BTreeMap<String, usize>> {
    let mut counts = BTreeMap::new();
    let file = match File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(counts),
        Err(error) => return Err(error.into()),
    };
    for line in BufReader::new(file).lines() {
        let record: SwitchRecord = serde_json::from_str(&line?)?;
        *counts.entry(record.path.label().to_string()).or_default() += 1;
    }
    Ok(counts)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn switch_log_line_has_the_focus_receipt() {
        let record = SwitchRecord {
            timestamp: 1_234,
            from: Some("garden/claude".to_string()),
            to: Some("hub/codex".to_string()),
            path: SwitchPath::Queue,
            scenario: "busy-morning".to_string(),
            waiting_count: 5,
        };
        let line = record.json_line().unwrap();
        assert!(line.ends_with('\n'));
        assert_eq!(
            serde_json::from_str::<SwitchRecord>(line.trim()).unwrap(),
            record
        );
        assert!(line.contains("\"path\":\"queue\""));
        assert!(line.contains("\"timestamp\":1234"));
    }
}
