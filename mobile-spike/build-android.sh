#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
: "${ATTN_SPIKE_CLIENT_TOKEN:=$(cat "$HOME/.attn-phonespike/client-token")}"
export ATTN_SPIKE_CLIENT_TOKEN ATTN_SPIKE_WS_URL="${ATTN_SPIKE_WS_URL:-ws://localhost:28889/ws}"
npx vite build
npx cap sync android
(cd android && ./gradlew assembleDebug -q)
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
adb reverse tcp:28889 tcp:28889
