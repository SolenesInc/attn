use std::env;
use std::path::PathBuf;

fn main() {
    let target_os = env::var("CARGO_CFG_TARGET_OS").expect("target os");
    let target_arch = env::var("CARGO_CFG_TARGET_ARCH").expect("target arch");
    let platform = match (target_os.as_str(), target_arch.as_str()) {
        ("macos", "aarch64") => "darwin_arm64",
        ("linux", "x86_64") => "linux_amd64",
        ("linux", "aarch64") => "linux_arm64",
        _ => panic!("attn-pty-host does not support {target_os}/{target_arch}"),
    };
    let repo = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("manifest dir"))
        .parent()
        .expect("pty-host has repo parent")
        .to_path_buf();
    let native = repo.join("third_party/ghostty-vt").join(platform);

    cc::Build::new()
        .file("src/ghostty_bridge.c")
        .include(native.join("include"))
        .warnings(true)
        .compile("attn_ghostty_bridge");

    println!(
        "cargo:rustc-link-search=native={}",
        native.join("lib").display()
    );
    println!("cargo:rustc-link-lib=static=ghostty-vt");
    println!("cargo:rerun-if-changed=src/ghostty_bridge.c");
    println!("cargo:rerun-if-changed=../ghostty-vt.pin");
    println!(
        "cargo:rerun-if-changed={}",
        native.join("include").display()
    );
    println!(
        "cargo:rerun-if-changed={}",
        native.join("lib/libghostty-vt.a").display()
    );
    println!("cargo:rerun-if-env-changed=ATTN_PTY_HOST_SNAPSHOT_FORMAT");

    if target_os == "macos" {
        for framework in ["CoreFoundation", "CoreText", "CoreGraphics", "Foundation"] {
            println!("cargo:rustc-link-lib=framework={framework}");
        }
    } else if target_os == "linux" {
        println!("cargo:rustc-link-lib=m");
        println!("cargo:rustc-link-lib=pthread");
        println!("cargo:rustc-link-lib=util");
    } else {
        panic!("attn-pty-host supports macOS and Linux, got {target_os}");
    }

    println!(
        "cargo:rustc-env=ATTN_PTY_HOST_SNAPSHOT_FORMAT={}",
        env::var("ATTN_PTY_HOST_SNAPSHOT_FORMAT").unwrap_or_else(|_| "dev".to_owned())
    );
}
