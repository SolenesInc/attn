#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/test-go.sh --packages -v -run '^Test(AnAuthorsInstalledHarnessLaunchesTakesInputAndSurvivesADaemonRestart|ThePluginSDKPassesItsOwnTests)$' ./cmd/attnd
