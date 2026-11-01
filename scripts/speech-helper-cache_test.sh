#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-speech-cache-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
export XDG_CACHE_HOME="$work/cache with spaces" SPEECH_TEST_BUILDS="$work/builds" CARGO_HOME="$work/cargo-home"
: > "$SPEECH_TEST_BUILDS"
mkdir -p "$work/bin" "$work/repo/scripts/lib" "$work/repo/app/speech/src"
cp "$root/scripts/build-speech-helper.sh" "$work/repo/scripts/"
cp "$root/scripts/lib/build-cache.sh" "$work/repo/scripts/lib/"
cp "$root/rust-toolchain.toml" "$work/repo/"
cat > "$work/bin/cargo" <<'CARGO'
#!/usr/bin/env bash
set -euo pipefail
crate="$(dirname "$3")"
mkdir -p "$crate/target/release"
cat "$crate/src/main.rs" > "$crate/target/release/attn-speech"
chmod +x "$crate/target/release/attn-speech"
echo build >> "$SPEECH_TEST_BUILDS"
CARGO
printf '#!/usr/bin/env bash\nprintf "rustc fixture\\nhost: fixture\\n"\n' > "$work/bin/rustc"
chmod +x "$work/bin/cargo" "$work/bin/rustc"
export PATH="$work/bin:$PATH"

printf '[package]\nname = "attn-speech"\n' > "$work/repo/app/speech/Cargo.toml"
printf 'lock\n' > "$work/repo/app/speech/Cargo.lock"
printf 'old\n' > "$work/repo/app/speech/src/main.rs"
printf '/app/speech/target/\n' > "$work/repo/.gitignore"
git -C "$work/repo" init -q
git -C "$work/repo" add .
git -C "$work/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm fixture
git -C "$work/repo" worktree add -q --detach "$work/second" HEAD

builds() { wc -l < "$SPEECH_TEST_BUILDS" | tr -d ' '; }
helper() { cat "$work/$1/app/speech/target/release/attn-speech"; }

"$work/repo/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 1 && "$(helper repo)" == old ]]

"$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 1 ]] || { echo 'the second worktree rebuilt unchanged speech sources' >&2; exit 1; }
[[ "$(helper second)" == old && -x "$work/second/app/speech/target/release/attn-speech" ]]

printf 'new\n' > "$work/second/app/speech/src/main.rs"
"$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 2 && "$(helper second)" == new && "$(helper repo)" == old ]]

git -C "$work/second" checkout -q -- app/speech/src/main.rs
"$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 2 && "$(helper second)" == old ]]

TRANSCRIBE_CMAKE_ARGS=-DGGML_NATIVE=ON "$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 3 ]] || { echo 'a native build setting reused a binary built without it' >&2; exit 1; }

CARGO_PROFILE_RELEASE_OPT_LEVEL=0 "$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 4 ]] || { echo 'a Cargo profile override reused a binary built without it' >&2; exit 1; }

mkdir -p "$work/second/.cargo"
printf '[profile.release]\nlto = false\n' > "$work/second/.cargo/config.toml"
"$work/second/scripts/build-speech-helper.sh" > /dev/null
[[ "$(builds)" == 5 ]] || { echo 'a Cargo config file reused a binary built without it' >&2; exit 1; }
echo 'speech helper cache: OK'
