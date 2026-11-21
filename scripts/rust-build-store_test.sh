#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-rust-build-store-test.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
export XDG_CACHE_HOME="$work/cache with spaces"
store="$XDG_CACHE_HOME/attn/rust-builds"

mkdir -p "$work/bin" "$work/repo/scripts/lib" "$work/repo/cli/attn/src"
cp "$root/scripts/rust-cli.sh" "$work/repo/scripts/"
cp "$root/scripts/lib/rust-build-store.sh" "$root/scripts/lib/libghostty-vt.sh" "$work/repo/scripts/lib/"
cp "$root/rust-toolchain.toml" "$work/repo/"
printf '[workspace]\nmembers = ["attn"]\nresolver = "2"\n' > "$work/repo/cli/Cargo.toml"
printf '[package]\nname = "attn"\nversion = "0.0.0"\nedition = "2021"\n' > "$work/repo/cli/attn/Cargo.toml"
printf 'pub fn greeting() -> &'"'"'static str { "%s" }\n' first > "$work/repo/cli/attn/src/lib.rs"
printf 'fn main() { println!("{}", attn::greeting()); }\n' > "$work/repo/cli/attn/src/main.rs"
printf 'outside\n' > "$work/repo/outside.txt"
printf '/cli/target/\n' > "$work/repo/.gitignore"
(cd "$work/repo/cli" && cargo generate-lockfile --offline -q)

real_cargo="$(command -v cargo)"
printf '#!/usr/bin/env bash\necho build >> "%s"\nexec "%s" "$@"\n' "$work/cargo-runs" "$real_cargo" > "$work/bin/cargo"
chmod +x "$work/bin/cargo"
export PATH="$work/bin:$PATH"
cargo_runs() { [[ -f "$work/cargo-runs" ]] && wc -l < "$work/cargo-runs" | tr -d ' ' || echo 0; }

git -C "$work/repo" init -q
git -C "$work/repo" add .
git -C "$work/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm fixture
git -C "$work/repo" worktree add -q --detach "$work/second" HEAD

bash "$work/repo/scripts/rust-cli.sh" build --out "$work/out/first/attn" 2> "$work/first.log" || { cat "$work/first.log"; exit 1; }
[[ "$("$work/out/first/attn")" == first ]]
[[ "$(cargo_runs)" == 1 ]]

bash "$work/second/scripts/rust-cli.sh" build --out "$work/out/second/attn"
[[ "$("$work/out/second/attn")" == first ]]
[[ "$(cargo_runs)" == 1 ]] || { echo 'the second worktree rebuilt a CLI the store already held' >&2; exit 1; }
[[ "$(bash "$work/second/scripts/rust-cli.sh" entry)" == "$store/attn/$(bash "$work/repo/scripts/rust-cli.sh" key)" ]]

printf 'pub fn greeting() -> &'"'"'static str { "%s" }\n' second > "$work/second/cli/attn/src/lib.rs"
bash "$work/second/scripts/rust-cli.sh" build --out "$work/out/second/attn" 2> "$work/second.log" || { cat "$work/second.log"; exit 1; }
[[ "$("$work/out/second/attn")" == second ]]
[[ "$(cargo_runs)" == 2 ]]

stored_before="$(find "$store/attn" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)"
printf 'pub fn greeting() -> &'"'"'static str { include_str!("../../../outside.txt") }\n' > "$work/second/cli/attn/src/lib.rs"
if bash "$work/second/scripts/lib/rust-build-store.sh" provide attn 2> "$work/outside.log"; then
  echo 'the store kept a build that read a file outside its input roots' >&2
  exit 1
fi
grep -qF "$work/second/outside.txt" "$work/outside.log" || { cat "$work/outside.log"; exit 1; }
[[ "$(find "$store/attn" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)" == "$stored_before" ]]
[[ "$(cargo_runs)" == 3 ]]

printf 'pub fn greeting() -> &'"'"'static str { option_env!("ATTN_STORE_PROBE").unwrap_or("unset") }\n' > "$work/second/cli/attn/src/lib.rs"
if ATTN_STORE_PROBE=set bash "$work/second/scripts/lib/rust-build-store.sh" provide attn 2> "$work/env.log"; then
  echo 'the store kept a build that read an undeclared variable' >&2
  exit 1
fi
grep -q '^  ATTN_STORE_PROBE$' "$work/env.log" || { cat "$work/env.log"; exit 1; }
[[ "$(find "$store/attn" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort)" == "$stored_before" ]]
unset_probe="$(bash "$work/second/scripts/lib/rust-build-store.sh" provide attn 2>/dev/null)"
[[ "$("$unset_probe")" == unset ]]
[[ "$(cargo_runs)" == 5 ]]

first_entry="$(bash "$work/repo/scripts/rust-cli.sh" entry)"
second_entry="$(dirname "$unset_probe")"
touch -t 202001010000 "$first_entry" "$second_entry"
bash "$work/repo/scripts/rust-cli.sh" build --out "$work/out/first/attn"
[[ "$(cargo_runs)" == 5 ]] || { echo 'a stored CLI rebuilt after its entry aged' >&2; exit 1; }
printf 'pub fn greeting() -> &'"'"'static str { "%s" }\n' third > "$work/second/cli/attn/src/lib.rs"
bash "$work/second/scripts/rust-cli.sh" build --out "$work/out/second/attn" 2>/dev/null
[[ "$(cargo_runs)" == 6 ]]
[[ -d "$first_entry" ]] || { echo 'eviction removed an entry a hit had just used' >&2; exit 1; }
[[ ! -e "$second_entry" ]] || { echo 'an entry untouched for 7 days survived a store write' >&2; exit 1; }
echo 'rust build store: OK'
