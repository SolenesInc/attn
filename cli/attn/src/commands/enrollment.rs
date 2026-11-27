use super::support::{client, parse_or_exit};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_json, print_stdout};
use crate::paths::{dir, join, os_error_text};
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::json;
use attn::go::quote::quote;
use attn::go::time::Time;
use attn_protocol::DropOutpostCopyMessage;
use serde::{Deserialize, Serialize};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Write as _};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};

const HELP: &str = r#"usage: attn enrollment <command>

An outpost's data folder is enrolled to the home it first accepts a bridge
from, and the outpost refuses bridges from any other home. A home has an id of
its own, apart from the id of the daemon that runs it.

commands:
  status [--json]
        show this daemon's id, and whether its data folder is a home or an
        outpost enrolled to another home.

  leave [--home <home-id>] [--json]
        release the enrollment so the data folder is its own again. The
        daemon closes the bridge from the old home and stops the terminals it
        ran for it: right away when it is running, otherwise when it next
        starts. Remove the outpost from the old home first; a home that still
        has it pairs again on its next connection. With --home, release only
        an enrollment to that home; an enrollment to any other home is kept
        and the result's status is "kept".
"#;

const DAEMON_ID_FILE: &str = "daemon-id";
const RECORD_FILE: &str = "enrollment.json";
const NO_RECORD: &str = "no enrollment record";
const NO_DAEMON_ID: &str = "no daemon id";
const DAEMON_ID_LENGTH: usize = 34;
const RFC3339: &str = "2006-01-02T15:04:05Z07:00";

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().map(String::as_str) else {
        return status(run, &[]);
    };
    match verb {
        "-h" | "--help" | "help" => {
            print_stdout(HELP);
            Ok(())
        }
        "status" => {
            run.path = "enrollment status".to_string();
            status(run, &args[1..])
        }
        "leave" => {
            run.path = "enrollment leave".to_string();
            leave(run, &args[1..])
        }
        "--json" => status(run, args),
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!(
                "enrollment: unknown command {}\n{HELP}",
                quote(verb.as_bytes())
            );
            Err(Stop(2))
        }
    }
}

const ROLE_HOME: &str = "home";
const ROLE_OUTPOST: &str = "outpost";

#[derive(Default)]
struct Status {
    daemon_id: String,
    record: Record,
}

impl Status {
    fn is_home(&self) -> bool {
        self.record.role == ROLE_HOME
    }

    fn home_label(&self) -> String {
        let id = if self.record.home_id.is_empty() {
            &self.record.home_daemon_id
        } else {
            &self.record.home_id
        };
        if self.record.home_name.is_empty() {
            id.clone()
        } else {
            format!("{} ({id})", self.record.home_name)
        }
    }

    fn describe(&self) -> String {
        match self.record.role.as_str() {
            ROLE_HOME => "home".to_string(),
            ROLE_OUTPOST => format!("enrolled to {}", self.home_label()),
            _ => "unknown".to_string(),
        }
    }
}

enum LoadError {
    NoRecord,
    NoDaemonId,
    Other(String),
}

impl LoadError {
    fn text(&self) -> &str {
        match self {
            LoadError::NoRecord => NO_RECORD,
            LoadError::NoDaemonId => NO_DAEMON_ID,
            LoadError::Other(text) => text,
        }
    }
}

#[derive(Serialize, Deserialize, Default, Clone)]
struct Record {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    role: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    home_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    home_name: String,
    #[serde(default)]
    home_daemon_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    replaced_daemon_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    recorded_at: String,
}

fn valid_id(id: &str, prefix: &str) -> bool {
    id.len() == DAEMON_ID_LENGTH
        && id.starts_with(prefix)
        && id[2..].bytes().all(|byte| byte.is_ascii_hexdigit())
}

fn valid_daemon_id(id: &str) -> bool {
    valid_id(id, "d-")
}

fn valid_home_id(id: &str) -> bool {
    valid_id(id, "h-")
}

const HEX_DIGITS: &[u8; 16] = b"0123456789abcdef";

fn new_home_id() -> Result<String, String> {
    use std::io::Read as _;
    let mut bytes = [0u8; 16];
    File::open("/dev/urandom")
        .and_then(|mut random| random.read_exact(&mut bytes))
        .map_err(|error| format!("generate home id: {}", os_error_text(&error)))?;
    let mut id = String::from("h-");
    for byte in bytes {
        id.push(HEX_DIGITS[usize::from(byte >> 4)] as char);
        id.push(HEX_DIGITS[usize::from(byte & 0x0f)] as char);
    }
    Ok(id)
}

fn read_daemon_id(root: &str) -> Result<String, LoadError> {
    let path = join(root, DAEMON_ID_FILE);
    match fs::read(&path) {
        Ok(data) => {
            let id = String::from_utf8_lossy(&data).trim().to_string();
            if valid_daemon_id(&id) {
                Ok(id)
            } else {
                Err(LoadError::NoDaemonId)
            }
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => Err(LoadError::NoDaemonId),
        Err(error) => Err(LoadError::Other(format!(
            "read daemon id: open {path}: {}",
            os_error_text(&error)
        ))),
    }
}

fn read_record(path: &str, daemon_id: &str) -> Result<Record, LoadError> {
    let data = match fs::read(path) {
        Ok(data) => data,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Err(LoadError::NoRecord),
        Err(error) => {
            return Err(LoadError::Other(format!(
                "read enrollment record: open {path}: {}",
                os_error_text(&error)
            )));
        }
    };
    let mut record: Record = serde_json::from_slice(&data)
        .map_err(|error| LoadError::Other(format!("parse enrollment record {path}: {error}")))?;
    if !record.home_daemon_id.is_empty() && !valid_daemon_id(&record.home_daemon_id) {
        return Err(LoadError::Other(format!(
            "enrollment record {path} names an invalid home daemon id {}",
            quote(record.home_daemon_id.as_bytes())
        )));
    }
    if !record.replaced_daemon_id.is_empty() && !valid_daemon_id(&record.replaced_daemon_id) {
        return Err(LoadError::Other(format!(
            "enrollment record {path} names an invalid replaced daemon id {}",
            quote(record.replaced_daemon_id.as_bytes())
        )));
    }
    let invalid_home_id = || {
        LoadError::Other(format!(
            "enrollment record {path} names an invalid home id {}",
            quote(record.home_id.as_bytes())
        ))
    };
    match record.role.as_str() {
        "" => {
            if record.home_daemon_id.is_empty() || !record.home_id.is_empty() {
                return Err(LoadError::Other(format!(
                    "enrollment record {path} has no role"
                )));
            }
            if record.home_daemon_id == daemon_id {
                record.role = ROLE_HOME.to_string();
                record.home_daemon_id.clear();
                record.home_name.clear();
            } else {
                record.role = ROLE_OUTPOST.to_string();
            }
        }
        ROLE_HOME => {
            if !record.home_id.is_empty() && !valid_home_id(&record.home_id) {
                return Err(invalid_home_id());
            }
            record.home_daemon_id.clear();
        }
        ROLE_OUTPOST => {
            if !valid_home_id(&record.home_id) {
                return Err(invalid_home_id());
            }
            record.home_daemon_id.clear();
        }
        role => {
            return Err(LoadError::Other(format!(
                "enrollment record {path} has unknown role {}",
                quote(role.as_bytes())
            )));
        }
    }
    Ok(record)
}

fn load(root: &str) -> Result<Status, (String, LoadError)> {
    let daemon_id = match read_daemon_id(root) {
        Ok(id) => id,
        Err(LoadError::NoDaemonId) => String::new(),
        Err(error) => return Err((String::new(), error)),
    };
    match read_record(&join(root, RECORD_FILE), &daemon_id) {
        Ok(record) => Ok(Status { daemon_id, record }),
        Err(error) => Err((daemon_id, error)),
    }
}

#[derive(Serialize)]
struct StatusError<'a> {
    daemon_id: &'a str,
    error: &'a str,
}

#[derive(Serialize)]
struct StatusJson<'a> {
    daemon_id: &'a str,
    role: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    home_id: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    home_name: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    home_daemon_id: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    replaced_daemon_id: &'a str,
    enrollment: String,
    is_home: bool,
}

fn print_encoded<T: Serialize>(value: &T) {
    if let Ok(compact) = json::encode(value) {
        print_json(&compact);
    }
}

fn status(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "enrollment status",
        &[Flag {
            name: "json",
            default: FlagDefault::Bool(false),
            usage: "print the status as JSON",
        }],
    );
    parse_or_exit(run, &mut flags, args)?;
    let json_output = flags.bool("json");
    let root = run.paths.attn_dir();
    let status = match load(&root) {
        Ok(status) => status,
        Err((partial, error)) => {
            if json_output {
                print_encoded(&StatusError {
                    daemon_id: &partial,
                    error: error.text(),
                });
            } else {
                eprintln!("enrollment: {}", error.text());
                if matches!(error, LoadError::NoRecord) {
                    eprintln!(
                        "No daemon has started in {root} yet; the record is written at startup."
                    );
                }
            }
            return Err(Stop(1));
        }
    };
    if json_output {
        print_encoded(&StatusJson {
            daemon_id: &status.daemon_id,
            role: &status.record.role,
            home_id: &status.record.home_id,
            home_name: &status.record.home_name,
            home_daemon_id: &status.record.home_daemon_id,
            replaced_daemon_id: &status.record.replaced_daemon_id,
            enrollment: status.describe(),
            is_home: status.is_home(),
        });
        return Ok(());
    }
    let mut out = format!(
        "daemon:     {}\nenrollment: {}\n",
        status.daemon_id,
        status.describe()
    );
    if !status.record.home_id.is_empty() {
        out.push_str("home id:    ");
        out.push_str(&status.record.home_id);
        out.push('\n');
    }
    if !status.is_home() {
        out.push_str(
            "\nThis data folder is enrolled to another home; run `attn enrollment leave` to release it.\n",
        );
    }
    print_stdout(&out);
    Ok(())
}

#[derive(Serialize)]
struct LeaveResult {
    status: &'static str,
    daemon_id: String,
    home_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    previous_home_id: String,
    message: String,
}

fn leave_record(root: &str, home: &str) -> Result<LeaveResult, LoadError> {
    let daemon_id = read_daemon_id(root)?;
    let record_path = join(root, RECORD_FILE);
    let _lock = lock(&record_path).map_err(LoadError::Other)?;
    let current = match read_record(&record_path, &daemon_id) {
        Ok(record) => Status {
            daemon_id: daemon_id.clone(),
            record,
        },
        Err(LoadError::NoRecord) => Status::default(),
        Err(error) => return Err(error),
    };
    if current.is_home() {
        return Ok(LeaveResult {
            status: "unchanged",
            home_id: current.record.home_id,
            daemon_id,
            previous_home_id: String::new(),
            message: "not enrolled to another home".to_string(),
        });
    }
    let previous = if current.record.home_id.is_empty() {
        current.record.home_daemon_id.clone()
    } else {
        current.record.home_id.clone()
    };
    if !home.is_empty() && !previous.is_empty() && previous != home {
        return Ok(LeaveResult {
            status: "kept",
            message: format!(
                "enrolled to home {}, not {home}; kept the enrollment",
                current.home_label()
            ),
            home_id: previous,
            daemon_id,
            previous_home_id: String::new(),
        });
    }
    let home_id = new_home_id().map_err(LoadError::Other)?;
    write_record(&record_path, &home_id, &daemon_id).map_err(LoadError::Other)?;
    Ok(LeaveResult {
        status: "left",
        home_id,
        daemon_id,
        previous_home_id: previous,
        message: "no longer enrolled to another home".to_string(),
    })
}

fn create_private_dir(path: &str) -> io::Result<()> {
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(path)
}

fn lock(path: &str) -> Result<File, String> {
    let name = crate::paths::base(path);
    create_private_dir(&dir(path))
        .map_err(|error| format!("create data root: {}", os_error_text(&error)))?;
    let lock_path = format!("{path}.lock");
    let file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .mode(0o600)
        .open(&lock_path)
        .map_err(|error| {
            format!(
                "open {name} lock: open {lock_path}: {}",
                os_error_text(&error)
            )
        })?;
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX) } != 0 {
        let error = io::Error::last_os_error();
        return Err(format!("lock {name}: {}", os_error_text(&error)));
    }
    Ok(file)
}

fn write_record(path: &str, home_id: &str, daemon_id: &str) -> Result<(), String> {
    let record = Record {
        role: ROLE_HOME.to_string(),
        home_id: home_id.to_string(),
        home_daemon_id: daemon_id.to_string(),
        recorded_at: Time::now_utc().format(RFC3339),
        ..Record::default()
    };
    let compact =
        json::encode(&record).map_err(|error| format!("encode enrollment record: {error}"))?;
    let mut data = json::indent(&compact);
    data.push(b'\n');
    write_atomic(path, &data)
        .map_err(|error| format!("persist enrollment record: {}", os_error_text(&error)))
}

fn write_atomic(path: &str, data: &[u8]) -> io::Result<()> {
    let nanos = Time::now_utc().unix_nanos();
    let temporary = format!("{path}.tmp.{}.{nanos}", std::process::id());
    OpenOptions::new()
        .create(true)
        .write(true)
        .truncate(true)
        .mode(0o600)
        .open(&temporary)?
        .write_all(data)?;
    fs::rename(&temporary, path).inspect_err(|_| {
        let _ = fs::remove_file(&temporary);
    })
}

fn leave(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "enrollment leave",
        &[
            Flag {
                name: "json",
                default: FlagDefault::Bool(false),
                usage: "print the result as JSON",
            },
            Flag {
                name: "home",
                default: FlagDefault::String(""),
                usage: "release only an enrollment to this home id",
            },
        ],
    );
    parse_or_exit(run, &mut flags, args)?;
    let home = flags.string("home").to_string();
    if !home.is_empty() && !valid_home_id(&home) && !valid_daemon_id(&home) {
        eprintln!(
            "enrollment leave: --home {} is not a home id; attn enrollment status on the home prints its id",
            quote(home.as_bytes())
        );
        return Err(Stop(2));
    }
    let root = run.paths.attn_dir();
    let result = leave_record(&root, &home).map_err(|error| {
        eprintln!("enrollment leave: {}", error.text());
        if matches!(error, LoadError::NoDaemonId) {
            eprintln!("No daemon has started in {root} yet, so there is nothing to release.");
        }
        Stop(1)
    })?;
    let daemon = client(run);
    if result.status != "kept" && daemon.is_running() {
        daemon
            .send(&DropOutpostCopyMessage::new())
            .map_err(|error| {
                run.record_send_error(&error);
                eprintln!(
                    "enrollment leave: released the enrollment, but the daemon could not stop the terminals it ran for its home: {error}"
                );
                Stop(1)
            })?;
    }
    if flags.bool("json") {
        print_encoded(&result);
        return Ok(());
    }
    print_stdout(&format!("{}\n", result.message));
    Ok(())
}
