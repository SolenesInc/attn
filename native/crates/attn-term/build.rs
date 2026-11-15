use std::env;
use std::path::PathBuf;
use std::process::Command;

fn main() {
    let target_os = env::var("CARGO_CFG_TARGET_OS").expect("target os");
    let target_arch = env::var("CARGO_CFG_TARGET_ARCH").expect("target arch");
    let platform = match (target_os.as_str(), target_arch.as_str()) {
        ("macos", "aarch64") => "darwin_arm64",
        ("linux", "x86_64") => "linux_amd64",
        ("linux", "aarch64") => "linux_arm64",
        _ => panic!("attn-term has no libghostty-vt for {target_os}/{target_arch}"),
    };
    let repo = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("manifest dir"))
        .join("../../..")
        .canonicalize()
        .expect("the attn checkout holds native/");
    let native = repo.join("third_party/ghostty-vt").join(platform);
    let archive = native.join("lib/libghostty-vt.a");
    assert!(
        archive.is_file(),
        "attn-term links {}, which is missing: run `make {}` from the checkout's root",
        archive.display(),
        archive.strip_prefix(&repo).unwrap().display(),
    );

    cc::Build::new()
        .file("src/bridge.c")
        .include(native.join("include"))
        .warnings(true)
        .compile("attn_term_bridge");
    println!(
        "cargo:rustc-link-search=native={}",
        native.join("lib").display()
    );
    println!("cargo:rustc-link-lib=static=ghostty-vt");
    if target_os == "macos" {
        for framework in ["CoreFoundation", "CoreText", "CoreGraphics", "Foundation"] {
            println!("cargo:rustc-link-lib=framework={framework}");
        }
    } else {
        println!("cargo:rustc-link-lib=m");
        println!("cargo:rustc-link-lib=pthread");
    }

    let script = repo.join("scripts/snapshot-format.sh");
    let output = Command::new("bash")
        .arg(&script)
        .output()
        .expect("run scripts/snapshot-format.sh");
    assert!(
        output.status.success(),
        "scripts/snapshot-format.sh failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let format = String::from_utf8(output.stdout).expect("a UTF-8 snapshot format");
    println!("cargo:rustc-env=ATTN_SNAPSHOT_FORMAT={}", format.trim());

    println!("cargo:rerun-if-changed=src/bridge.c");
    println!("cargo:rerun-if-changed={}", archive.display());
    println!(
        "cargo:rerun-if-changed={}",
        native.join("include").display()
    );
    println!("cargo:rerun-if-changed={}", script.display());
    for lock in ["ghostty-vt-native.lock", "ghostty-vt-wasm.lock"] {
        println!("cargo:rerun-if-changed={}", repo.join(lock).display());
    }
}
