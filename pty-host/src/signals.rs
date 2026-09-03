use std::time::{Duration, Instant};

const KEEPALIVE: Duration = Duration::from_secs(1);
const MAX_PENDING: usize = 64 * 1024;

pub struct Observation {
    pub claim: &'static str,
    pub detail: String,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Kind {
    Claude,
    Codex,
    Shell,
    None,
}

pub struct SignalObserver {
    kind: Kind,
    pending: Vec<u8>,
    last_claim: String,
    last_emit: Option<Instant>,
}

impl SignalObserver {
    pub fn new(agent: &str) -> Self {
        let kind = match agent.trim().to_ascii_lowercase().as_str() {
            "claude" | "claude-code" => Kind::Claude,
            "codex" => Kind::Codex,
            "shell" => Kind::Shell,
            _ => Kind::None,
        };
        Self {
            kind,
            pending: Vec::new(),
            last_claim: String::new(),
            last_emit: None,
        }
    }

    pub fn observe(&mut self, chunk: &[u8]) -> Vec<Observation> {
        if self.kind == Kind::None || chunk.is_empty() {
            return Vec::new();
        }
        self.pending.extend_from_slice(chunk);
        if self.pending.len() > MAX_PENDING {
            let keep = self.pending.len().min(32);
            self.pending.drain(..self.pending.len() - keep);
        }

        let mut frames = Vec::new();
        let mut cursor = 0;
        let mut keep_from = None;
        while let Some(relative) = find_bytes(&self.pending[cursor..], b"\x1b]") {
            let start = cursor + relative;
            let payload_start = start + 2;
            let Some((end, terminator_len)) = find_osc_end(&self.pending, payload_start) else {
                keep_from = Some(start);
                break;
            };
            frames.push(String::from_utf8_lossy(&self.pending[payload_start..end]).into_owned());
            cursor = end + terminator_len;
        }
        if let Some(start) = keep_from {
            self.pending.drain(..start);
        } else {
            let keep = usize::from(self.pending.ends_with(b"\x1b"));
            let discard = self.pending.len().saturating_sub(keep);
            self.pending.drain(..discard);
        }

        let mut out = Vec::new();
        for frame in frames {
            if let Some(observation) = self.classify(&frame) {
                out.push(observation);
            }
        }
        out
    }

    pub fn observe_shell_poll(
        &mut self,
        shell_pgid: i32,
        foreground_pgid: i32,
    ) -> Option<Observation> {
        if self.kind != Kind::Shell || foreground_pgid <= 0 {
            return None;
        }
        if foreground_pgid == shell_pgid {
            self.emit("not_busy", "shell at prompt".to_owned(), false)
        } else {
            self.emit("busy", "foreground command running".to_owned(), false)
        }
    }

    fn classify(&mut self, frame: &str) -> Option<Observation> {
        let (code, payload) = frame.split_once(';').unwrap_or((frame, ""));
        if self.kind == Kind::Shell && code == "133" {
            return self.classify_shell_marker(payload);
        }
        if code != "0" && code != "2" {
            return None;
        }
        match self.kind {
            Kind::Claude => {
                classify_claude(payload).and_then(|(claim, detail)| self.emit(claim, detail, false))
            }
            Kind::Codex => {
                classify_codex(payload).and_then(|(claim, detail)| self.emit(claim, detail, false))
            }
            Kind::Shell | Kind::None => None,
        }
    }

    fn classify_shell_marker(&mut self, payload: &str) -> Option<Observation> {
        let marker = payload.split(';').next().unwrap_or_default();
        match marker {
            "A" => self.emit("not_busy", "shell at prompt".to_owned(), false),
            "C" => {
                let detail = payload
                    .split(';')
                    .find_map(|part| part.strip_prefix("cmdline_url="))
                    .map_or_else(
                        || "command started".to_owned(),
                        |command| format!("command started: {}", truncate(command, 80)),
                    );
                self.emit("busy", detail, true)
            }
            "D" => {
                let detail = payload
                    .split(';')
                    .nth(1)
                    .filter(|code| !code.is_empty())
                    .map_or_else(
                        || "command finished".to_owned(),
                        |code| format!("command exited {code}"),
                    );
                self.emit("not_busy", detail, true)
            }
            _ => None,
        }
    }

    fn emit(&mut self, claim: &'static str, detail: String, edge: bool) -> Option<Observation> {
        let now = Instant::now();
        if !edge
            && self.last_claim == claim
            && self
                .last_emit
                .is_some_and(|at| now.duration_since(at) < KEEPALIVE)
        {
            return None;
        }
        self.last_claim.clear();
        self.last_claim.push_str(claim);
        self.last_emit = Some(now);
        Some(Observation { claim, detail })
    }
}

fn classify_claude(title: &str) -> Option<(&'static str, String)> {
    let trimmed = title.trim();
    let first = trimmed.chars().next()?;
    if first == '✳' {
        return Some(("not_busy", strip_status_glyph(trimmed)));
    }
    if is_status_glyph(first) {
        return Some(("busy", strip_status_glyph(trimmed)));
    }
    Some(("unclassified", trimmed.to_owned()))
}

fn classify_codex(title: &str) -> Option<(&'static str, String)> {
    let trimmed = title.trim();
    let first = trimmed.chars().next()?;
    if ('\u{2800}'..='\u{28ff}').contains(&first) {
        return Some(("busy", strip_status_glyph(trimmed)));
    }
    if trimmed.contains("Action Required") {
        let before = trimmed.split("Action Required").next().unwrap_or_default();
        let claim = if before.contains('.') {
            "approval"
        } else {
            "not_busy"
        };
        let detail = trimmed
            .split_once('|')
            .map_or(trimmed, |(_, rest)| rest)
            .trim()
            .to_owned();
        return Some((claim, detail));
    }
    Some(("not_busy", strip_status_glyph(trimmed)))
}

fn strip_status_glyph(value: &str) -> String {
    let trimmed = value.trim();
    let Some(first) = trimmed.chars().next() else {
        return String::new();
    };
    if is_status_glyph(first) {
        trimmed[first.len_utf8()..].trim().to_owned()
    } else {
        trimmed.to_owned()
    }
}

fn is_status_glyph(value: char) -> bool {
    matches!(value as u32, 0x2500..=0x28ff | 0x2b00..=0x2bff | 0x1f000..=0x1faff)
}

fn truncate(value: &str, limit: usize) -> String {
    if value.len() <= limit {
        return value.to_owned();
    }
    let mut end = limit;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    format!("{}…", &value[..end])
}

fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

fn find_osc_end(bytes: &[u8], start: usize) -> Option<(usize, usize)> {
    let mut cursor = start;
    while cursor < bytes.len() {
        if bytes[cursor] == 0x07 {
            return Some((cursor, 1));
        }
        if bytes[cursor] == 0x1b && bytes.get(cursor + 1) == Some(&b'\\') {
            return Some((cursor, 2));
        }
        cursor += 1;
    }
    None
}

#[cfg(test)]
mod tests {
    use super::SignalObserver;

    #[test]
    fn observes_split_codex_title() {
        let mut observer = SignalObserver::new("codex");
        assert!(observer.observe(b"\x1b]0;\xe2\xa0").is_empty());
        let observations = observer.observe(b"\x90 working\x07");
        assert_eq!(observations.len(), 1);
        assert_eq!(observations[0].claim, "busy");
    }

    #[test]
    fn shell_markers_are_edges() {
        let mut observer = SignalObserver::new("shell");
        let observations = observer.observe(b"\x1b]133;C;cmdline_url=make\x07\x1b]133;D;0\x07");
        assert_eq!(observations.len(), 2);
        assert_eq!(observations[0].claim, "busy");
        assert_eq!(observations[1].claim, "not_busy");
    }
}
