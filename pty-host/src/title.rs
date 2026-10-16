use std::path::Path;

#[derive(Clone, Default, serde::Deserialize, serde::Serialize)]
pub struct ShellTitle {
    pub in_command: bool,
    pub saw_marker: bool,
    pub command: String,
    pub prompt_title: String,
    pub program_title: String,
    pub folder: String,
}

impl ShellTitle {
    pub fn apply(&mut self, code: &str, payload: &str) {
        match code {
            "133" => {
                self.saw_marker = true;
                match payload.split(';').next().unwrap_or_default() {
                    "C" => {
                        self.in_command = true;
                        self.command = command_line(payload)
                            .map(|command| bounded_title(&command))
                            .unwrap_or_default();
                        self.program_title.clear();
                    }
                    "A" | "D" => {
                        self.in_command = false;
                        self.command.clear();
                        self.program_title.clear();
                    }
                    _ => {}
                }
            }
            "0" | "2" => {
                if self.in_command {
                    self.program_title = bounded_title(payload);
                } else {
                    self.prompt_title = bounded_title(payload);
                }
            }
            "7" => {
                if let Some(rest) = payload.strip_prefix("file://")
                    && let Some(start) = rest.find('/')
                    && let Some(path) = percent_decode(&rest[start..])
                {
                    let folder = Path::new(&path)
                        .file_name()
                        .and_then(|part| part.to_str())
                        .unwrap_or(&path);
                    self.folder = bounded_title(folder);
                }
            }
            _ => {}
        }
    }

    pub fn needs_process_name(&self) -> bool {
        !self.saw_marker || (self.in_command && self.command.is_empty())
    }

    pub fn poll(&mut self, foreign: bool, process_name: &str) {
        if !self.saw_marker {
            if self.in_command != foreign {
                self.program_title.clear();
            }
            self.in_command = foreign;
            self.command = if foreign {
                bounded_title(process_name)
            } else {
                String::new()
            };
        } else if self.in_command && self.command.is_empty() {
            self.command = bounded_title(process_name);
        }
    }

    pub fn title(&self) -> String {
        let title = if self.in_command {
            if self.program_title.is_empty() {
                &self.command
            } else {
                &self.program_title
            }
        } else if self.prompt_title.is_empty() {
            Path::new(&self.folder)
                .file_name()
                .and_then(|part| part.to_str())
                .unwrap_or(&self.folder)
        } else {
            &self.prompt_title
        };
        bounded_title(title)
    }
}

fn bounded_title(title: &str) -> String {
    title.chars().take(256).collect()
}

pub fn command_line(payload: &str) -> Option<String> {
    payload
        .split(';')
        .find_map(|part| part.strip_prefix("cmdline_url=").and_then(percent_decode))
        .or_else(|| {
            payload
                .split(';')
                .find_map(|part| part.strip_prefix("cmdline=").map(str::to_owned))
        })
}

fn percent_decode(text: &str) -> Option<String> {
    let mut out = Vec::new();
    let mut bytes = text.bytes();
    while let Some(byte) = bytes.next() {
        if byte == b'%' {
            let high = char::from(bytes.next()?).to_digit(16)?;
            let low = char::from(bytes.next()?).to_digit(16)?;
            out.push(u8::try_from(high * 16 + low).ok()?);
        } else {
            out.push(byte);
        }
    }
    String::from_utf8(out).ok()
}

#[cfg(target_os = "linux")]
pub fn process_name(pid: i32) -> String {
    std::fs::read_to_string(format!("/proc/{pid}/comm"))
        .unwrap_or_default()
        .trim()
        .to_owned()
}

#[cfg(target_os = "macos")]
pub fn process_name(pid: i32) -> String {
    #[link(name = "proc")]
    unsafe extern "C" {
        fn proc_name(pid: i32, buffer: *mut libc::c_void, size: u32) -> i32;
    }
    let mut buffer = [0u8; 256];
    let count = unsafe { proc_name(pid, buffer.as_mut_ptr().cast(), 256) };
    let count = usize::try_from(count).unwrap_or_default();
    String::from_utf8_lossy(&buffer[..count])
        .trim_end_matches('\0')
        .to_owned()
}

#[cfg(test)]
mod tests {
    use super::ShellTitle;
    use crate::signals::SignalObserver;
    use base64::{Engine as _, engine::general_purpose::STANDARD};

    #[derive(serde::Deserialize)]
    struct Event {
        data: Option<String>,
        foreign: Option<bool>,
        process: Option<String>,
    }

    #[derive(serde::Deserialize)]
    struct Corpus {
        name: String,
        folder: String,
        events: Vec<Event>,
        titles: Vec<String>,
    }

    #[test]
    fn recorded_prompt_title_timelines() {
        let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../internal/pty/testdata/shell-titles");
        for entry in std::fs::read_dir(root).unwrap() {
            let path = entry.unwrap().path();
            if path.extension().is_none_or(|extension| extension != "json") {
                continue;
            }
            let corpus: Corpus = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
            for bytewise in [false, true] {
                let mut observer = SignalObserver::new("shell", &corpus.folder);
                let mut titles = vec![observer.title.title()];
                for event in &corpus.events {
                    if let Some(foreign) = event.foreign {
                        observer
                            .title
                            .poll(foreign, event.process.as_deref().unwrap_or_default());
                        let title = observer.title.title();
                        if titles.last() != Some(&title) {
                            titles.push(title);
                        }
                    } else if let Some(data) = &event.data {
                        let data = STANDARD.decode(data).unwrap();
                        if bytewise {
                            for byte in data {
                                observer.observe(&[byte], || None);
                                titles.extend(observer.take_title_changes());
                            }
                        } else {
                            observer.observe(&data, || None);
                            titles.extend(observer.take_title_changes());
                        }
                    }
                }
                assert_eq!(titles, corpus.titles, "{} bytewise={bytewise}", corpus.name);
            }
        }
    }

    #[test]
    fn titles_are_capped_at_unicode_characters() {
        for size in [0, 1, 255, 256, 257, 1024] {
            let title = "🦊".repeat(size);
            let mut state = ShellTitle::default();
            state.apply("0", &title);
            assert_eq!(state.title(), "🦊".repeat(size.min(256)));
            assert_eq!(state.prompt_title, "🦊".repeat(size.min(256)));
            state.apply("133", &format!("C;cmdline={title}"));
            assert_eq!(state.command, "🦊".repeat(size.min(256)));
            state.apply("2", &title);
            assert_eq!(state.program_title, "🦊".repeat(size.min(256)));
            state = ShellTitle::default();
            state.poll(true, &title);
            assert_eq!(state.command, "🦊".repeat(size.min(256)));
            if size > 0 {
                state.apply("7", &format!("file:///tmp/{title}"));
                assert_eq!(state.folder, "🦊".repeat(size.min(256)));
            }
        }
    }
}
