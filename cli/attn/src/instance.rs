use crate::paths::{clean, dir, join, os_error_text};
use attn::go::quote::quote;
use serde_json::Value;
use std::cell::OnceCell;
use std::fmt::Write as _;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;

const INSTANCE_PATTERN_TEXT: &str = "^[a-z0-9][a-z0-9-]{0,15}$";

const FNV_OFFSET: u32 = 2_166_136_261;
const FNV_PRIME: u32 = 16_777_619;
const DERIVED_PORT_BASE: u32 = 20_000;
const DERIVED_PORT_SPAN: u32 = 9_849;
const DEFAULT_PORT: &str = "9849";
const DEV_PORT: &str = "29849";

pub type Getenv<'a> = &'a dyn Fn(&str) -> String;

#[derive(Default)]
struct ConfigFile {
    db_path: String,
    socket_path: String,
}

pub struct Paths<'a> {
    env: Getenv<'a>,
    config: OnceCell<ConfigFile>,
}

fn matches_instance_pattern(name: &str) -> bool {
    let bytes = name.as_bytes();
    if bytes.is_empty() || bytes.len() > 16 {
        return false;
    }
    let alnum = |b: u8| b.is_ascii_lowercase() || b.is_ascii_digit();
    alnum(bytes[0]) && bytes[1..].iter().all(|&b| alnum(b) || b == b'-')
}

impl<'a> Paths<'a> {
    pub fn new(env: Getenv<'a>) -> Self {
        Self {
            env,
            config: OnceCell::new(),
        }
    }

    fn get(&self, name: &str) -> String {
        (self.env)(name)
    }

    fn trimmed(&self, name: &str) -> String {
        self.get(name).trim().to_string()
    }

    pub fn instance(&self) -> String {
        let raw = self.trimmed("ATTN_INSTANCE");
        let normalized = raw.to_lowercase();
        if raw.is_empty() || !matches_instance_pattern(&normalized) {
            return String::new();
        }
        normalized
    }

    pub fn instance_label(&self) -> String {
        let instance = self.instance();
        if instance.is_empty() {
            "default".to_string()
        } else {
            instance
        }
    }

    pub fn validate_instance(&self) -> Result<(), String> {
        let trimmed = self.trimmed("ATTN_INSTANCE");
        if trimmed.is_empty() || matches_instance_pattern(&trimmed.to_lowercase()) {
            return Ok(());
        }
        Err(format!(
            "invalid ATTN_INSTANCE={}: must match {INSTANCE_PATTERN_TEXT}",
            quote(trimmed.as_bytes())
        ))
    }

    fn home(&self) -> Option<String> {
        let home = self.get("HOME");
        (!home.is_empty()).then_some(home)
    }

    fn attn_dir(&self) -> String {
        let data_dir = self.trimmed("ATTN_DATA_DIR");
        if !data_dir.is_empty() {
            return clean(&data_dir);
        }
        self.data_dir_for_named(&self.instance())
    }

    fn data_dir_for_named(&self, instance: &str) -> String {
        let base = self
            .home()
            .map_or_else(|| "/tmp/.attn".to_string(), |home| join(&home, ".attn"));
        if instance.is_empty() {
            base
        } else {
            format!("{base}-{instance}")
        }
    }

    pub fn data_dir_for_instance(&self, instance: &str) -> String {
        let name = instance.trim().to_lowercase();
        if name.is_empty() || name == "default" || !matches_instance_pattern(&name) {
            return self.data_dir_for_named("");
        }
        self.data_dir_for_named(&name)
    }

    pub fn socket_path_for_instance(&self, instance: &str) -> String {
        join(&self.data_dir_for_instance(instance), "attn.sock")
    }

    pub fn config_path(&self) -> String {
        let path = self.trimmed("ATTN_CONFIG_PATH");
        if path.is_empty() {
            join(&self.attn_dir(), "config.json")
        } else {
            clean(&path)
        }
    }

    fn config(&self) -> &ConfigFile {
        self.config.get_or_init(|| {
            let configured = self.get("ATTN_CONFIG_PATH");
            let path = if configured.is_empty() {
                join(&self.attn_dir(), "config.json")
            } else {
                configured
            };
            let Ok(data) = fs::read(path) else {
                return ConfigFile::default();
            };
            let parsed: Value = serde_json::from_slice(&data).unwrap_or(Value::Null);
            let field = |key: &str| {
                parsed
                    .get(key)
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string()
            };
            ConfigFile {
                db_path: field("db_path"),
                socket_path: field("socket_path"),
            }
        })
    }

    pub fn socket_path(&self) -> String {
        let from_env = self.get("ATTN_SOCKET_PATH");
        if !from_env.is_empty() {
            return from_env;
        }
        let configured = &self.config().socket_path;
        if !configured.is_empty() {
            return configured.clone();
        }
        join(&self.attn_dir(), "attn.sock")
    }

    fn db_path(&self) -> String {
        let from_env = self.get("ATTN_DB_PATH");
        if !from_env.is_empty() {
            return from_env;
        }
        let configured = &self.config().db_path;
        if !configured.is_empty() {
            return configured.clone();
        }
        join(&self.attn_dir(), "attn.db")
    }

    fn plugin_dir(&self) -> String {
        let path = self.trimmed("ATTN_PLUGIN_DIR");
        if path.is_empty() {
            join(&self.attn_dir(), "plugins")
        } else {
            path
        }
    }

    fn ws_port(&self) -> String {
        let port = self.trimmed("ATTN_WS_PORT");
        if port.is_empty() {
            ws_port_for_instance(&self.instance())
        } else {
            port
        }
    }

    pub fn collapse_home(&self, path: &str) -> String {
        let Some(home) = self.home() else {
            return path.to_string();
        };
        let home = clean(&home);
        let cleaned = clean(path);
        if cleaned == home {
            return "~".to_string();
        }
        match cleaned.strip_prefix(&format!("{home}/")) {
            Some(rest) => format!("~/{rest}"),
            None => path.to_string(),
        }
    }

    pub fn banner(&self) -> Option<String> {
        let instance = self.instance();
        if instance.is_empty() {
            return None;
        }
        Some(format!(
            "[attn instance={instance} socket={} port={}]",
            self.collapse_home(&self.socket_path()),
            self.ws_port()
        ))
    }

    fn harness_notebook_root(&self) -> String {
        if self.trimmed("ATTN_HARNESS_DATA_DIR").is_empty() {
            return String::new();
        }
        clean(&self.trimmed("ATTN_HARNESS_NOTEBOOK_ROOT"))
    }

    pub fn validate_routing(&self) -> Result<(), String> {
        self.validate_harness_routing()?;
        if !self.trimmed("ATTN_HARNESS_DATA_DIR").is_empty() {
            return Ok(());
        }
        let instance = self.instance();
        if instance.is_empty() {
            return Ok(());
        }
        let instance_dir = self.data_dir_for_instance(&instance);
        let instance_port = ws_port_for_instance(&instance);

        let path_check =
            |env: &'static str, config_key: &'static str, resolved: String, file: &str| Check {
                env,
                config_key,
                resolved,
                expected: join(&instance_dir, file),
                is_path: true,
            };
        let checks = [
            Check {
                env: "ATTN_DATA_DIR",
                config_key: "",
                resolved: self.attn_dir(),
                expected: instance_dir.clone(),
                is_path: true,
            },
            path_check(
                "ATTN_SOCKET_PATH",
                "socket_path",
                self.socket_path(),
                "attn.sock",
            ),
            path_check("ATTN_DB_PATH", "db_path", self.db_path(), "attn.db"),
            path_check("ATTN_CONFIG_PATH", "", self.config_path(), "config.json"),
            path_check("ATTN_PLUGIN_DIR", "", self.plugin_dir(), "plugins"),
            Check {
                env: "ATTN_WS_PORT",
                config_key: "",
                resolved: self.ws_port(),
                expected: instance_port.clone(),
                is_path: false,
            },
        ];

        let mut conflicts = Vec::new();
        let mut data_dir_conflict = false;
        for check in checks {
            let agree =
                values_agree(&check.resolved, &check.expected, check.is_path).map_err(|error| {
                    format!(
                        "resolve {} for instance {instance}: {}",
                        check.env,
                        os_error_text(&error)
                    )
                })?;
            if agree {
                continue;
            }
            if check.env == "ATTN_DATA_DIR" {
                data_dir_conflict = true;
            }
            let override_value = self.trimmed(check.env);
            if !override_value.is_empty() {
                conflicts.push(Conflict {
                    label: check.env,
                    value: override_value,
                    source: ConflictSource::Env,
                });
                continue;
            }
            if data_dir_conflict || check.config_key.is_empty() {
                continue;
            }
            conflicts.push(Conflict {
                label: check.env,
                value: check.resolved,
                source: ConflictSource::Config {
                    key: check.config_key,
                    file: self.config_path(),
                },
            });
        }
        if conflicts.is_empty() {
            return Ok(());
        }
        Err(format_conflict(
            &instance,
            &instance_dir,
            &instance_port,
            &conflicts,
        ))
    }

    fn validate_harness_routing(&self) -> Result<(), String> {
        let raw_root = self.trimmed("ATTN_HARNESS_DATA_DIR");
        if raw_root.is_empty() {
            return Ok(());
        }
        if !raw_root.starts_with('/') {
            return Err("ATTN_HARNESS_DATA_DIR must be absolute".to_string());
        }
        let metadata = fs::symlink_metadata(&raw_root).map_err(|error| {
            format!(
                "inspect ATTN_HARNESS_DATA_DIR: lstat {raw_root}: {}",
                os_error_text(&error)
            )
        })?;
        if !metadata.is_dir() || metadata.permissions().mode() & 0o077 != 0 {
            return Err("ATTN_HARNESS_DATA_DIR must be a direct owner-only directory".to_string());
        }
        let resolve = |label: &str, path: &str| {
            canonical_runtime_path(path)
                .map_err(|error| format!("resolve {label}: {}", os_error_text(&error)))
        };

        let root = resolve("ATTN_HARNESS_DATA_DIR", &raw_root)?;
        let production = resolve("production data directory", &self.data_dir_for_instance(""))?;
        if within(&root, &production) || within(&production, &root) {
            return Err(format!(
                "refusing harness root {} because it overlaps production {}",
                quote(root.as_bytes()),
                quote(production.as_bytes())
            ));
        }
        let instance = self.instance();
        if !instance.is_empty() {
            let canonical = resolve(
                &format!("instance {instance} data directory"),
                &self.data_dir_for_instance(&instance),
            )?;
            if within(&root, &canonical) || within(&canonical, &root) {
                return Err(format!(
                    "refusing harness root {} because it overlaps instance {instance} data directory {}",
                    quote(root.as_bytes()),
                    quote(canonical.as_bytes())
                ));
            }
        }

        for (label, path) in [
            ("ATTN_DATA_DIR", self.attn_dir()),
            ("ATTN_SOCKET_PATH", self.socket_path()),
            ("ATTN_DB_PATH", self.db_path()),
            ("ATTN_CONFIG_PATH", self.config_path()),
            ("ATTN_PLUGIN_DIR", self.plugin_dir()),
            ("ATTN_HARNESS_NOTEBOOK_ROOT", self.harness_notebook_root()),
        ] {
            let resolved = resolve(&format!("{label} for instance harness"), &path)?;
            if !within(&resolved, &root) {
                return Err(format!(
                    "refusing {label}={} outside instance harness root {}",
                    quote(resolved.as_bytes()),
                    quote(root.as_bytes())
                ));
            }
        }
        let port = self.ws_port();
        let port = port.trim();
        if port == DEFAULT_PORT
            || port == DEV_PORT
            || (!instance.is_empty() && port == ws_port_for_instance(&instance))
        {
            return Err(format!("refusing instance harness websocket port {port}"));
        }
        Ok(())
    }
}

struct Check {
    env: &'static str,
    config_key: &'static str,
    resolved: String,
    expected: String,
    is_path: bool,
}

enum ConflictSource {
    Env,
    Config { key: &'static str, file: String },
}

struct Conflict {
    label: &'static str,
    value: String,
    source: ConflictSource,
}

fn format_conflict(
    instance: &str,
    instance_dir: &str,
    instance_port: &str,
    conflicts: &[Conflict],
) -> String {
    let mut text = format!(
        "ATTN_INSTANCE={instance} disagrees with the routing this process resolved.\n  instance {instance} is {instance_dir} (port {instance_port}), but:\n"
    );
    let mut env_names = Vec::new();
    let mut files = Vec::new();
    for conflict in conflicts {
        match &conflict.source {
            ConflictSource::Env => {
                let _ = writeln!(text, "    {:<16} = {}", conflict.label, conflict.value);
                env_names.push(conflict.label);
            }
            ConflictSource::Config { key, file } => {
                let _ = writeln!(
                    text,
                    "    {:<16} = {} ({key} in {file})",
                    conflict.label, conflict.value
                );
                files.push(file.as_str());
            }
        }
    }
    let _ = writeln!(
        text,
        "  An explicit override outranks ATTN_INSTANCE, so this process would act as instance {instance} against another instance's data. Refusing before anything opens it."
    );
    if !env_names.is_empty() {
        let scrub = env_names.iter().fold(String::new(), |mut scrub, name| {
            let _ = write!(scrub, " -u {name}");
            scrub
        });
        let _ = writeln!(text, "  Fix: env{scrub} ATTN_INSTANCE={instance} <command>");
        let _ = writeln!(
            text,
            "  Or clear them in your shell: eval \"$(attn instance-env {instance})\""
        );
    }
    if let Some(file) = files.first() {
        let _ = writeln!(
            text,
            "  No environment change fixes {file}: edit it, or start the instance over with `attn instance clean {instance}`"
        );
    }
    text.trim_end_matches('\n').to_string()
}

fn ws_port_for_instance(instance: &str) -> String {
    let name = instance.trim().to_lowercase();
    match name.as_str() {
        "" | "default" => DEFAULT_PORT.to_string(),
        "dev" => DEV_PORT.to_string(),
        other if !matches_instance_pattern(other) => DEFAULT_PORT.to_string(),
        other => (DERIVED_PORT_BASE + fnv1a(other) % DERIVED_PORT_SPAN).to_string(),
    }
}

fn fnv1a(text: &str) -> u32 {
    text.bytes().fold(FNV_OFFSET, |hash, byte| {
        (hash ^ u32::from(byte)).wrapping_mul(FNV_PRIME)
    })
}

fn within(candidate: &str, root: &str) -> bool {
    candidate == root || root == "/" || candidate.starts_with(&format!("{root}/"))
}

fn values_agree(resolved: &str, expected: &str, is_path: bool) -> io::Result<bool> {
    if !is_path {
        return Ok(resolved.trim() == expected.trim());
    }
    Ok(canonical_runtime_path(resolved)? == canonical_runtime_path(expected)?)
}

fn canonical_runtime_path(path: &str) -> io::Result<String> {
    let trimmed = path.trim();
    if trimmed.is_empty() {
        return Ok(String::new());
    }
    let absolute = if trimmed.starts_with('/') {
        clean(trimmed)
    } else {
        join(&std::env::current_dir()?.to_string_lossy(), trimmed)
    };
    let mut existing = absolute.clone();
    let mut missing: Vec<String> = Vec::new();
    loop {
        match fs::canonicalize(&existing) {
            Ok(resolved) => {
                let mut result = resolved.to_string_lossy().into_owned();
                for part in missing.iter().rev() {
                    result = join(&result, part);
                }
                return Ok(clean(&result));
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let parent = dir(&existing);
                if parent == existing {
                    return Ok(absolute);
                }
                let name = existing[existing.rfind('/').map_or(0, |slash| slash + 1)..].to_string();
                missing.push(name);
                existing = parent;
            }
            Err(error) => return Err(error),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;
    use std::collections::BTreeMap;
    use std::path::{Path, PathBuf};

    #[derive(Deserialize)]
    struct Case {
        name: String,
        env: BTreeMap<String, String>,
        #[serde(default)]
        dirs: Vec<String>,
        #[serde(default)]
        owner_only_dirs: Vec<String>,
        #[serde(default)]
        files: BTreeMap<String, String>,
        want: Want,
    }

    #[derive(Deserialize)]
    struct Want {
        instance: String,
        label: String,
        data_dir: String,
        socket_path: String,
        config_path: String,
        port: String,
        banner: Option<String>,
        validate_instance: Option<String>,
        validate_routing: Option<String>,
    }

    struct Scratch(PathBuf);

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn scratch_root(index: usize) -> Scratch {
        let base = std::env::temp_dir().join(format!(
            "attn-instance-corpus-{}-{index}",
            std::process::id()
        ));
        fs::create_dir_all(&base).unwrap();
        Scratch(fs::canonicalize(&base).unwrap())
    }

    fn materialize(text: &str, root: &str, home: &str) -> String {
        text.replace("$ROOT", root).replace("$HOME", home)
    }

    #[test]
    fn matches_the_go_corpus() {
        let corpus = std::fs::read_to_string(
            Path::new(env!("CARGO_MANIFEST_DIR")).join("../testdata/instance.jsonl"),
        )
        .unwrap();
        let cases: Vec<Case> = corpus
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        assert!(cases.len() > 20, "corpus has {} cases", cases.len());

        for (index, case) in cases.iter().enumerate() {
            let scratch = scratch_root(index);
            let root = scratch.0.to_str().unwrap().to_string();
            let home = format!("{root}/home");
            fs::create_dir_all(&home).unwrap();
            for directory in &case.dirs {
                fs::create_dir_all(materialize(directory, &root, &home)).unwrap();
            }
            for directory in &case.owner_only_dirs {
                let path = materialize(directory, &root, &home);
                fs::create_dir_all(&path).unwrap();
                fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).unwrap();
            }
            for (path, content) in &case.files {
                fs::write(
                    materialize(path, &root, &home),
                    materialize(content, &root, &home),
                )
                .unwrap();
            }
            let mut env: BTreeMap<String, String> = case
                .env
                .iter()
                .map(|(key, value)| (key.clone(), materialize(value, &root, &home)))
                .collect();
            env.entry("HOME".to_string())
                .or_insert_with(|| home.clone());
            let getenv = move |name: &str| env.get(name).cloned().unwrap_or_default();
            let paths = Paths::new(&getenv);

            let expect = |text: &str| materialize(text, &root, &home);
            let want = &case.want;
            let name = &case.name;
            assert_eq!(paths.instance(), want.instance, "{name}: instance");
            assert_eq!(paths.instance_label(), want.label, "{name}: label");
            assert_eq!(paths.attn_dir(), expect(&want.data_dir), "{name}: data dir");
            assert_eq!(
                paths.socket_path(),
                expect(&want.socket_path),
                "{name}: socket"
            );
            assert_eq!(
                paths.config_path(),
                expect(&want.config_path),
                "{name}: config"
            );
            assert_eq!(paths.ws_port(), want.port, "{name}: port");
            assert_eq!(
                paths.banner(),
                want.banner.as_deref().map(expect),
                "{name}: banner"
            );
            assert_eq!(
                paths.validate_instance().err(),
                want.validate_instance.as_deref().map(expect),
                "{name}: validate instance"
            );
            assert_eq!(
                paths.validate_routing().err(),
                want.validate_routing.as_deref().map(expect),
                "{name}: validate routing"
            );
        }
    }
}
