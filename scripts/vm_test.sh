#!/usr/bin/env bash
# Tests for scripts/vm: agents sharing one checkout and one VM keep their
# instances apart, and one agent finishing leaves the others untouched.
# Run: bash scripts/vm_test.sh
#
# macvm is fake: the guest is a directory, and each call's holder is FAKE_HOLDER.
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/vm-test.XXXXXX")"
trap 'rm -rf "$sandbox"' EXIT

pass=0
fail=0

expect() {
  local label="$1"
  shift
  if "$@"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $label"
  fi
}
absent() { ! [ -e "$1" ]; }
differ() { [ "$1" != "$2" ]; }
same() { [ "$1" = "$2" ]; }
says() { grep -qF "$2" <<<"$1"; }

checkout="$sandbox/attn--shared"
guest="$sandbox/guest"
mkdir -p "$sandbox/bin" "$checkout/scripts" "$checkout/app/node_modules/ws" "$guest"
cp "$script_dir/vm" "$script_dir/source-fingerprint.sh" "$checkout/scripts/"
mkdir -p "$checkout/native/src"
printf 'fn main() {}\n' >"$checkout/native/src/main.rs"
printf 'node_modules/\ntarget/\n' >"$checkout/.gitignore"
git -C "$checkout" init -q
git -C "$checkout" add .
git -C "$checkout" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -qm baseline

cat >"$sandbox/bin/attn" <<'FAKE'
#!/usr/bin/env bash
echo 9000
FAKE

cat >"$sandbox/bin/make" <<'FAKE'
#!/usr/bin/env bash
for arg in "$@"; do
  case "$arg" in
  INSTANCE=*) instance="${arg#INSTANCE=}" ;;
  esac
done
resources="native/target/staged/attn-$instance.app/Contents/Resources"
mkdir -p "$resources"
printf '{"sourceFingerprint":"%s"}\n' "$(bash scripts/source-fingerprint.sh --app native)" >"$resources/build-identity.json"
echo "$instance" >>"$FAKE_BUILDS"
FAKE

cat >"$sandbox/bin/macvm" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
guest_run() {
  cd "$FAKE_GUEST"
  case "$1" in
  osascript)
    name="${3#quit app \"}"
    rm -f "running/${name%\"}"
    ;;
  open)
    mkdir -p running
    name="$(basename "${@: -1}")"
    touch "running/${name%.app}"
    ;;
  env) echo '{"frontendReady": true}' ;;
  /Users/admin/*) rm -rf ".attn-$4" ;;
  *) "$@" ;;
  esac
}
case "$1" in
holder) echo "$FAKE_HOLDER" ;;
up)
  touch "$FAKE_GUEST/.claim-$FAKE_HOLDER"
  echo macvm-1
  ;;
down) rm -f "$FAKE_GUEST/.claim-$FAKE_HOLDER" ;;
exec)
  shift
  [ -e "$FAKE_GUEST/.claim-$FAKE_HOLDER" ] || exit 1
  guest_run "$@"
  ;;
push) mkdir -p "$FAKE_GUEST/$2" ;;
install-app)
  [ -z "${FAKE_INSTALL_FAILS:-}" ] || exit 1
  name="$(basename "$2")"
  instance="${name#attn-}"
  mkdir -p "$FAKE_GUEST/Applications" "$FAKE_GUEST/.attn-${instance%.app}"
  rm -rf "$FAKE_GUEST/Applications/$name"
  cp -R "$2" "$FAKE_GUEST/Applications/$name"
  ;;
*) exit 2 ;;
esac
FAKE
chmod +x "$sandbox/bin/"*
export PATH="$sandbox/bin:$PATH" FAKE_GUEST="$guest" FAKE_BUILDS="$sandbox/builds"

as() {
  local who="$1"
  shift
  FAKE_HOLDER="$who" bash "$checkout/scripts/vm" "$@" 2>&1
}
instance_from() { sed -n 's/.*scripts\/vm down --instance //p' <<<"$1"; }
live() { [ -e "$guest/running/attn-$1" ] && [ -d "$guest/.attn-$1" ] && [ -d "$guest/Applications/attn-$1.app" ]; }
gone() { ! [ -e "$guest/running/attn-$1" ] && ! [ -e "$guest/.attn-$1" ] && ! [ -e "$guest/Applications/attn-$1.app" ]; }

# Two agents, one checkout, the default name.
out_a="$(as attn-a up)"
out_b="$(as attn-b up)"
a="$(instance_from "$out_a")"
b="$(instance_from "$out_b")"
expect "agent A gets an instance" differ "$a" ""
expect "agents sharing a checkout get different default instances" differ "$a" "$b"
expect "A's default name is within attn's 16 characters" [ "${#a}" -le 16 ]
expect "A's default stays the same across calls" same "$a" "$(instance_from "$(as attn-a up)")"
expect "A can drive its own instance" says "$(as attn-a drive ping)" frontendReady

as attn-a down >/dev/null
expect "A's finish cleans A's instance" gone "$a"
expect "A's finish leaves B's app and data" live "$b"
expect "B still drives its instance after A finished" says "$(as attn-b drive ping)" frontendReady
as attn-b down >/dev/null
expect "B's finish cleans B's instance" gone "$b"

# Two agents naming the same instance.
as attn-a up --instance shared >/dev/null
out="$(as attn-b up --instance shared)"
status=$?
expect "a taken name fails up" [ "$status" -ne 0 ]
expect "the refusal names the owner" says "$out" "belongs to attn-a"
expect "a refused up leaves the owner's app and data" live shared
out="$(as attn-b drive --instance shared ping)"
status=$?
expect "another agent cannot drive a taken name" [ "$status" -ne 0 ]
expect "the drive refusal names the owner" says "$out" "attn-a"
as attn-b down --instance shared >/dev/null
expect "another agent's down leaves the owner's app and data" live shared
expect "another agent's down releases its VM claim" absent "$guest/.claim-attn-b"
expect "the owner still drives after that" says "$(as attn-a drive --instance shared ping)" frontendReady
as attn-a down --instance shared >/dev/null
expect "the owner's down cleans the name" gone shared
as attn-b up --instance shared >/dev/null
expect "a released name can be taken by another agent" live shared
as attn-b down --instance shared >/dev/null

# A failed start is still the starter's to clean up.
FAKE_INSTALL_FAILS=1 as attn-a up --instance failed >/dev/null
status=$?
expect "a failed install fails up" [ "$status" -ne 0 ]
out="$(as attn-b up --instance failed)"
expect "a failed start still holds its name" says "$out" "belongs to attn-a"
as attn-b down --instance failed >/dev/null
as attn-a down --instance failed >/dev/null
as attn-b up --instance failed >/dev/null
expect "the starter's down frees the name" live failed
as attn-b down --instance failed >/dev/null

# An instance from a script that wrote no owner link, still held by its agent.
start_unowned() {
  touch "$guest/.claim-$1"
  mkdir -p "$guest/.attn-$2" "$guest/Applications/attn-$2.app" "$guest/running"
  touch "$guest/running/attn-$2"
}

start_unowned attn-earlier earlier
out="$(as attn-b up --instance earlier)"
status=$?
expect "up on an unowned instance fails" [ "$status" -ne 0 ]
expect "the refusal says how its owner takes it" says "$out" "scripts/vm adopt --instance earlier"
expect "a refused up leaves the unowned app and data" live earlier
out="$(as attn-b down --instance earlier)"
expect "down says the unowned instance stays" says "$out" "left in place"
expect "another agent's down leaves the unowned app and data" live earlier
expect "another agent's down releases its VM claim" absent "$guest/.claim-attn-b"
as attn-earlier adopt --instance earlier >/dev/null
expect "the owner's adopt keeps the app and data" live earlier
expect "the owner can drive after adopting" says "$(as attn-earlier drive --instance earlier ping)" frontendReady
out="$(as attn-b up --instance earlier)"
expect "an adopted name is refused to others" says "$out" "belongs to attn-earlier"
expect "that refusal leaves the adopted app and data" live earlier
out="$(as attn-b adopt --instance earlier)"
expect "adopt refuses an owned instance" says "$out" "belongs to attn-earlier"
as attn-b down --instance earlier >/dev/null
as attn-earlier down --instance earlier >/dev/null
expect "the owner's down cleans the adopted instance" gone earlier
touch "$guest/.claim-attn-b"
out="$(as attn-b adopt --instance nothing)"
expect "adopt names a missing instance" says "$out" "no instance 'nothing'"
expect "adopt on a missing instance takes nothing" absent "$guest/vm-instance-owners/nothing"
as attn-b down --instance nothing >/dev/null

# A change under native/ rebuilds and reinstalls the app; an unchanged tree builds it once.
builds_of() { grep -c "^$1$" "$FAKE_BUILDS"; }
guest_source() { jq -r .sourceFingerprint "$guest/Applications/attn-$1.app/Contents/Resources/build-identity.json"; }
as attn-a up --instance rebuild >/dev/null
as attn-a up --instance rebuild >/dev/null
expect "an unchanged tree builds the app once" same "$(builds_of rebuild)" 1
before="$(guest_source rebuild)"
printf 'fn main() { println!("changed"); }\n' >"$checkout/native/src/main.rs"
as attn-a up --instance rebuild >/dev/null
expect "a change under native/ rebuilds the app" same "$(builds_of rebuild)" 2
expect "a change under native/ reinstalls the app" differ "$(guest_source rebuild)" "$before"
as attn-a down --instance rebuild >/dev/null

echo "vm_test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
